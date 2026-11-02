// Package server HTTP 路由与处理器
package server

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"strings"
	"sync"

	"tbmusic/internal/cache"
	"tbmusic/internal/config"
	"tbmusic/internal/lxsource"
	"tbmusic/internal/music"
	"tbmusic/internal/music/kg"
	"tbmusic/internal/music/kw"
	"tbmusic/internal/music/mg"
	"tbmusic/internal/music/tx"
	"tbmusic/internal/music/wy"
	"tbmusic/internal/store"
)

type Server struct {
	Store      *store.Store
	Cfg        *config.Config
	ConfigPath string
	Sources    map[string]music.Source
	LX         *lxsource.Pool
	reloadMu   sync.Mutex
	adminMu    sync.Mutex // 串行化配置文件的读-改-写(白名单增删)
	Cache      *cache.Cache
	flightMu   sync.Mutex
	flights    map[string]*cacheFlight
	coverSem   chan struct{}
	StaticFS   fs.FS

	trustedNets []*net.IPNet  // server.trustedProxies 解析结果
	streamSem   chan struct{} // 音频代理并发闸门
}

// maxConcurrentStreams 同时进行的音频代理上限,防 fd/goroutine 被单方打满
const maxConcurrentStreams = 64

func New(st *store.Store, cfg *config.Config, cfgPath string, lx *lxsource.Pool) *Server {
	s := &Server{
		Store:      st,
		Cfg:        cfg,
		ConfigPath: cfgPath,
		Sources: map[string]music.Source{
			"kw": kw.Source{}, "tx": tx.Source{}, "wy": wy.Source{},
			"kg": kg.Source{}, "mg": mg.Source{},
		},
		LX:        lx,
		flights:   make(map[string]*cacheFlight),
		coverSem:  make(chan struct{}, 8),
		streamSem: make(chan struct{}, maxConcurrentStreams),
	}
	for _, cidr := range cfg.GetTrustedProxies() {
		_, n, err := net.ParseCIDR(cidr)
		if err != nil {
			log.Printf("[config] 忽略非法 trustedProxies 项 %q: %v", cidr, err)
			continue
		}
		s.trustedNets = append(s.trustedNets, n)
	}
	// 本机回环始终可信:反代与后端同机部署时对端恒为 127.0.0.1,
	// 若不采信其 X-Forwarded-For/Proto,所有请求会塌缩进同一个 IP 桶
	// (一人触发封禁即全站无法登录),Cookie 也拿不到 Secure 标记。
	// 回环对端只可能是本机进程,不存在外部伪造风险。
	for _, cidr := range []string{"127.0.0.0/8", "::1/128"} {
		if _, n, err := net.ParseCIDR(cidr); err == nil {
			s.trustedNets = append(s.trustedNets, n)
		}
	}
	if len(cfg.GetTrustedProxies()) == 0 {
		log.Printf("[config] 未配置 trustedProxies:仅本机回环被视为可信代理,若反代不在本机请补配该项")
	}
	return s
}

// Router 注册全部路由
func (s *Server) Router() http.Handler {
	mux := http.NewServeMux()

	// auth
	mux.HandleFunc("POST /api/auth/register", s.handleRegister)
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/auth/logout", s.authed(s.handleLogout))
	mux.HandleFunc("GET /api/auth/me", s.authed(s.handleMe))
	mux.HandleFunc("GET /api/auth/config", s.handleAuthConfig) // 公开:注册开关
	mux.HandleFunc("POST /api/auth/change-password", s.authed(s.handleChangePassword))

	// music
	mux.HandleFunc("GET /api/sources", s.authed(s.handleSources))
	mux.HandleFunc("GET /api/search", s.authed(s.handleSearch))
	mux.HandleFunc("GET /api/detail", s.authed(s.handleDetail))
	mux.HandleFunc("GET /api/toplists", s.authed(s.handleToplists))
	mux.HandleFunc("POST /api/music-url", s.authed(s.handleMusicURL))
	mux.HandleFunc("POST /api/lyric", s.authed(s.handleLyric))
	mux.HandleFunc("POST /api/pic", s.authed(s.handlePic))
	mux.HandleFunc("GET /api/cover", s.authed(s.handleCover))
	mux.HandleFunc("GET /api/stream", s.authed(s.handleStream))
	mux.HandleFunc("GET /api/random-song", s.authed(s.handleRandomSong))
	mux.HandleFunc("GET /api/random-songs", s.authed(s.handleRandomSongs))

	// library
	mux.HandleFunc("GET /api/playlists", s.authed(s.handleListPlaylists))
	mux.HandleFunc("POST /api/playlists", s.authed(s.handleCreatePlaylist))
	mux.HandleFunc("PATCH /api/playlists/{id}", s.authed(s.handleRenamePlaylist))
	mux.HandleFunc("DELETE /api/playlists/{id}", s.authed(s.handleDeletePlaylist))
	mux.HandleFunc("GET /api/playlists/{id}/songs", s.authed(s.handlePlaylistSongs))
	mux.HandleFunc("POST /api/playlists/{id}/songs", s.authed(s.handleAddPlaylistSong))
	mux.HandleFunc("DELETE /api/playlists/{id}/songs/{sid}", s.authed(s.handleRemovePlaylistSong))
	mux.HandleFunc("GET /api/blacklist", s.authed(s.handleListBlacklist))
	mux.HandleFunc("POST /api/blacklist", s.authed(s.handleAddBlacklist))
	mux.HandleFunc("DELETE /api/blacklist/{id}", s.authed(s.handleRemoveBlacklist))
	mux.HandleFunc("GET /api/history", s.authed(s.handleListHistory))
	mux.HandleFunc("POST /api/history", s.authed(s.handleAddHistory))
	mux.HandleFunc("DELETE /api/history", s.authed(s.handleClearHistory))

	// 管理员(超管)
	mux.HandleFunc("GET /api/admin/users", s.authedAdmin(s.handleListUsers))
	mux.HandleFunc("DELETE /api/admin/users/{id}", s.authedAdmin(s.handleDeleteUser))
	mux.HandleFunc("POST /api/admin/users/{id}/password", s.authedAdmin(s.handleResetUserPassword))
	mux.HandleFunc("PUT /api/admin/users/{id}/remark", s.authedAdmin(s.handleUpdateUserRemark))
	mux.HandleFunc("GET /api/admin/settings", s.authedAdmin(s.handleGetAdminSettings))
	mux.HandleFunc("GET /api/admin/cache", s.authedAdmin(s.handleCacheStats))
	mux.HandleFunc("PUT /api/admin/cache", s.authedAdmin(s.handleResizeCache))
	mux.HandleFunc("DELETE /api/admin/cache", s.authedAdmin(s.handleClearCache))
	mux.HandleFunc("PUT /api/admin/settings", s.authedAdmin(s.handlePutAdminSettings))
	// 采集脚本:单条重试加载(异步,返回后前端刷新状态)
	mux.HandleFunc("POST /api/admin/scripts/retry", s.authedAdmin(s.handleRetryScript))
	// 音频代理白名单:逐条增删改
	mux.HandleFunc("POST /api/admin/stream-hosts", s.authedAdmin(s.handleAddStreamHost))
	mux.HandleFunc("PUT /api/admin/stream-hosts", s.authedAdmin(s.handleUpdateStreamHost))
	mux.HandleFunc("DELETE /api/admin/stream-hosts", s.authedAdmin(s.handleRemoveStreamHost))

	mux.Handle("/", s.staticHandler())
	csrf := http.NewCrossOriginProtection()
	csrf.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusForbidden, "不允许跨站操作")
	}))
	return s.securityHeaders(csrf.Handler(mux))
}

// securityHeaders 全站统一安全响应头(需要 Server 判断是否 HTTPS,故为方法)
func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=(), payment=(), usb=()")
		// 允许自身脚本/样式与内联(Vue 需要),限制外域加载
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: https:; media-src "+s.mediaPolicy()+"; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'")
		// 仅在确认是 HTTPS 时下发 HSTS,避免明文访问场景下把浏览器锁死在错误协议上
		if s.requestIsHTTPS(r) {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		// 接口响应包含用户数据、邀请码、采集脚本地址等,禁止浏览器与中间代理缓存
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
			h.Set("Pragma", "no-cache")
		}
		next.ServeHTTP(w, r)
	})
}

/* ---------- 通用辅助 ---------- */

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) error {
	defer r.Body.Close()
	if ct := r.Header.Get("Content-Type"); ct != "" {
		mediaType, _, err := mime.ParseMediaType(ct)
		if err != nil || mediaType != "application/json" {
			return fmt.Errorf("请求必须使用 application/json")
		}
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		return err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("请求只能包含一个 JSON 对象")
	}
	return nil
}
