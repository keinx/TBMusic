package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/crypto/bcrypt"

	"tbmusic/internal/store"
)

const sessionCookie = "tbmusic_session"

// authed 要求已登录
func (s *Server) authed(next func(w http.ResponseWriter, r *http.Request, u *store.User)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, err := s.currentUser(w, r)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			log.Printf("[auth] 校验会话失败: %v", err)
			writeErr(w, http.StatusServiceUnavailable, "服务暂不可用,请稍后重试")
			return
		}
		if u == nil {
			writeErr(w, http.StatusUnauthorized, "未登录")
			return
		}
		if expected := r.Header.Get("X-TBMusic-User-ID"); expected != "" && expected != strconv.FormatInt(u.ID, 10) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "账户已变更，请重新确认操作", "code": "account_changed"})
			return
		}
		switch r.URL.Path {
		case "/api/search", "/api/detail", "/api/toplists", "/api/lyric", "/api/pic", "/api/cover", "/api/random-song":
			if !limiter.allow("metadata:"+strconv.FormatInt(u.ID, 10), 300, time.Minute) {
				writeErr(w, http.StatusTooManyRequests, "请求过于频繁，请稍后重试")
				return
			}
		}
		next(w, r, u)
	}
}

// authedAdmin 要求管理员
func (s *Server) authedAdmin(next func(w http.ResponseWriter, r *http.Request, u *store.User)) http.HandlerFunc {
	return s.authed(func(w http.ResponseWriter, r *http.Request, u *store.User) {
		if !u.IsAdmin {
			writeErr(w, http.StatusForbidden, "需要管理员权限")
			return
		}
		next(w, r, u)
	})
}

// currentUser 解析会话;发生滑动续期时重发 Cookie,否则浏览器仍按首次登录的 Max-Age 过期
func (s *Server) currentUser(w http.ResponseWriter, r *http.Request) (*store.User, error) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil, store.ErrNotFound
	}
	u, renewed, err := s.Store.SessionUser(c.Value)
	if err != nil {
		return nil, err
	}
	if renewed {
		s.setSessionCookie(w, r, c.Value)
	}
	return u, nil
}

var usernameRe = regexp.MustCompile(`^[\p{L}\p{N}_-]{2,24}$`)

func validUsername(name string) bool {
	return usernameRe.MatchString(name)
}

func validPassword(pw string) bool {
	if len(pw) < 6 || len(pw) > 64 {
		return false
	}
	for _, c := range pw {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}

type authReq struct {
	Username   string `json:"username"`
	Password   string `json:"password"`
	InviteCode string `json:"inviteCode"`
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	allowReg, _, _ := s.Cfg.GetAuthSettings()
	if !allowReg {
		writeErr(w, 403, "当前站点已关闭注册")
		return
	}
	var req authReq
	if err := decodeBody(w, r, &req); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	if !validUsername(req.Username) {
		writeErr(w, 400, "用户名需为 2-24 位字母/数字/中文/下划线")
		return
	}
	if !validPassword(req.Password) {
		writeErr(w, 400, "密码长度需为 6-64 位")
		return
	}
	// 注册限制:同一 IP 1 小时内最多注册 3 个账户(持久化,重启不失效)
	// 限流必须先于邀请码校验,否则邀请码可被不限速穷举
	ip := s.clientIP(r)
	if !limiter.allow("reg:"+ip, 5, time.Minute) {
		writeErr(w, 429, "尝试过于频繁,请稍后再试")
		return
	}
	_, inviteRequired, inviteCode := s.Cfg.GetAuthSettings()
	if inviteRequired {
		// 常量时间比较,避免时序侧信道
		if subtle.ConstantTimeCompare([]byte(req.InviteCode), []byte(inviteCode)) != 1 {
			writeErr(w, 403, "邀请码错误")
			return
		}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		writeErr(w, 500, "系统错误")
		return
	}
	u, err := s.Store.CreateRegisteredUser(req.Username, string(hash), ip)
	if err != nil {
		if errors.Is(err, store.ErrRegistrationLimit) {
			writeErr(w, 429, "注册过于频繁:同一 IP 一小时内最多注册 3 个账户")
		} else if store.IsDuplicate(err) {
			writeErr(w, 400, "用户名已被占用")
		} else {
			log.Printf("[auth] 注册失败: %v", err)
			writeErr(w, 500, "注册失败,请稍后重试")
		}
		return
	}
	if err := s.startSession(w, r, u.ID, string(hash)); err != nil {
		writeErr(w, 500, "登录状态创建失败,请重试")
		return
	}
	writeJSON(w, 200, map[string]any{"user": u})
}

// authLimiter 登录/注册按 IP 限流:滑动窗口计数,防 bcrypt CPU DoS 与爆破
type ipLimiter struct {
	mu        sync.Mutex
	hits      map[string][]time.Time
	lastSweep time.Time
}

const (
	// limiterMaxKeys 限流表单次最大 key 数(硬上限)
	limiterMaxKeys = 4096
	// limiterSweepInterval 两次全表清理的最小间隔(避免每个请求都 O(n) 遍历)
	limiterSweepInterval = 10 * time.Second
	// limiterRetain 一条记录的最长保留时间,必须 ≥ 各类限流窗口的最大值
	limiterRetain = 5 * time.Minute
)

var limiter = &ipLimiter{hits: map[string][]time.Time{}}

func (l *ipLimiter) allow(key string, max int, window time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	src := l.hits[key] // 原切片用于遍历;原地过滤写入同一底层数组
	kept := src[:0]
	for _, t := range src {
		if now.Sub(t) < window {
			kept = append(kept, t)
		}
	}
	allowed := len(kept) < max
	if allowed {
		l.hits[key] = append(kept, now)
	} else {
		// 拒绝路径也要写回:被压缩的窗口计数仍需保留,否则会退化成无限重试
		l.hits[key] = kept
	}
	l.sweepLocked(now)
	return allowed
}

// sweepLocked 维护限流表规模。
// 旧实现只在"放行"分支、且仅当表已超阈值时才清理,并且只删时间戳已过期的 key;
// 在大量不同来源 IP 持续冲击时(全部落在窗口内),map 会一直超阈值增长。
// 这里改为:按固定间隔无条件清理一次,清理后仍有超限则随机淘汰,保证有硬上界。
func (l *ipLimiter) sweepLocked(now time.Time) {
	if len(l.hits) <= limiterMaxKeys && now.Sub(l.lastSweep) < limiterSweepInterval {
		return
	}
	l.lastSweep = now
	// 1) 最近 limiterRetain 内没有任何记录的 key 直接丢弃
	for k, v := range l.hits {
		if len(v) == 0 || now.Sub(v[len(v)-1]) >= limiterRetain {
			delete(l.hits, k)
		}
	}
	// 2) 仍然超限:随机淘汰到上限(宁可丢部分计数,也不让内存无界增长)
	if excess := len(l.hits) - limiterMaxKeys; excess > 0 {
		for k := range l.hits {
			if excess <= 0 {
				break
			}
			delete(l.hits, k)
			excess--
		}
	}
}

// clientIP 提取限流用客户端 IP。
// 安全考量:默认不信任任何请求头(X-Real-IP/X-Forwarded-For 可被任意伪造绕过限流),
// 一律使用 TCP 对端地址;仅当对端命中 server.trustedProxies(如本机 Nginx)时,
// 才从 X-Forwarded-For 右侧起取第一个非可信地址。
func (s *Server) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer := net.ParseIP(host)
	if peer == nil {
		return "invalid" // 非法地址统一为一个桶,防怪异输入撑爆限流 map
	}
	if s.isTrustedProxy(peer) {
		if real := s.forwardedClientIP(r); real != nil {
			peer = real
		}
	}
	// IPv6 按 /64 前缀归并:防止同前缀内海量地址轮换绕过限流与封锁
	if peer.To4() == nil {
		return peer.Mask(net.CIDRMask(64, 128)).String()
	}
	return peer.String()
}

// isTrustedProxy 对端是否属于可信代理网段
func (s *Server) isTrustedProxy(ip net.IP) bool {
	for _, n := range s.trustedNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// forwardedClientIP 从 X-Forwarded-For 右侧向左取第一个非可信地址
func (s *Server) forwardedClientIP(r *http.Request) net.IP {
	xff := r.Header.Get("X-Forwarded-For")
	if xff == "" {
		if rip := r.Header.Get("X-Real-IP"); rip != "" {
			if ip := net.ParseIP(strings.TrimSpace(rip)); ip != nil {
				return ip
			}
		}
		return nil
	}
	parts := strings.Split(xff, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		ip := net.ParseIP(strings.TrimSpace(parts[i]))
		if ip == nil {
			continue
		}
		if s.isTrustedProxy(ip) {
			continue // 可信代理追加的地址继续往左看
		}
		return ip
	}
	return nil
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req authReq
	if err := decodeBody(w, r, &req); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	// 封禁检查:1 小时内密码错误 ≥5 次封锁 4 小时(持久化,重启不失效)
	ip := s.clientIP(r)
	if banned, until := s.Store.IPBanned(ip); banned {
		mins := (until-time.Now().Unix())/60 + 1
		writeErr(w, 403, fmt.Sprintf("登录失败次数过多,该 IP 已被封锁,请 %d 分钟后再试", mins))
		return
	}
	if !limiter.allow("login:"+ip, 10, time.Minute) {
		writeErr(w, 429, "尝试过于频繁,请稍后再试")
		return
	}
	u, hash, err := s.Store.GetUserByUsername(req.Username)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			// DB 故障不视为登录失败(防误封)
			log.Printf("[auth] 查询用户失败: %v", err)
			writeErr(w, 500, "服务暂不可用,请稍后再试")
			return
		}
		// 用户不存在时也执行一次 bcrypt 比较,消除时序侧信道枚举
		bcrypt.CompareHashAndPassword([]byte(dummyHash), []byte(req.Password))
		s.recordLoginFail(ip)
		writeErr(w, 401, "用户名或密码错误")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
		s.recordLoginFail(ip)
		writeErr(w, 401, "用户名或密码错误")
		return
	}
	// 登录成功:清空该 IP 的失败记录
	s.Store.ClearIPEvents("loginfail", ip)
	if err := s.startSession(w, r, u.ID, hash); err != nil {
		writeErr(w, 500, "登录状态创建失败,请重试")
		return
	}
	s.Store.EnsureDefaultPlaylist(u.ID)
	writeJSON(w, 200, map[string]any{"user": u})
}

// recordLoginFail 记录一次登录失败;1 小时窗口内累计第 5 次触发 4 小时封锁
func (s *Server) recordLoginFail(ip string) {
	s.Store.AddIPEvent("loginfail", ip)
	if s.Store.CountIPEvents("loginfail", ip) >= 5 {
		s.Store.BanIP(ip, time.Now().Add(4*time.Hour))
		s.Store.ClearIPEvents("loginfail", ip) // 已转封锁,清空失败明细
	}
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request, u *store.User) {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		if err := s.Store.DeleteSession(c.Value); err != nil {
			log.Printf("[auth] 撤销会话失败: %v", err)
			writeErr(w, 500, "退出失败,请重试")
			return
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request, u *store.User) {
	writeJSON(w, 200, map[string]any{"user": u})
}

// handleAuthConfig 公开的认证配置(注册开关等),登录遮罩需要
func (s *Server) handleAuthConfig(w http.ResponseWriter, r *http.Request) {
	allowReg, inviteReq, _ := s.Cfg.GetAuthSettings()
	writeJSON(w, 200, map[string]any{
		"allowRegister":  allowReg,
		"inviteRequired": inviteReq,
	})
}

// handleChangePassword 修改自己的密码
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req struct {
		OldPassword string `json:"oldPassword"`
		NewPassword string `json:"newPassword"`
	}
	if err := decodeBody(w, r, &req); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	if !validPassword(req.NewPassword) {
		writeErr(w, 400, "新密码长度需为 6-64 位")
		return
	}
	// 限流:bcrypt 是 CPU 密集型操作,即使已登录也不能无限调用
	if !limiter.allow("chpw:"+strconv.FormatInt(u.ID, 10), 10, 5*time.Minute) {
		writeErr(w, 429, "尝试过于频繁,请稍后再试")
		return
	}
	// 先校验旧密码:失败路径只花一次比对,不再为新密码做无谓的哈希
	verifiedHash, err := s.Store.VerifiedPasswordHash(u.ID, req.OldPassword)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			writeErr(w, 404, "用户不存在")
		case errors.Is(err, store.ErrBadPassword):
			writeErr(w, 400, "旧密码错误")
		default:
			log.Printf("[auth] 校验旧密码失败 uid=%d: %v", u.ID, err)
			writeErr(w, 500, "服务暂不可用,请稍后再试")
		}
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		writeErr(w, 500, "系统错误")
		return
	}
	if err := s.Store.ChangePasswordHash(u.ID, string(hash), verifiedHash); err != nil {
		if errors.Is(err, store.ErrPasswordChanged) {
			writeErr(w, http.StatusConflict, "密码已被修改，请重新登录后再试")
			return
		}
		log.Printf("[auth] 更新密码失败 uid=%d: %v", u.ID, err)
		writeErr(w, 500, "密码更新失败,请稍后再试")
		return
	}
	// 密码变更事务已吊销全部旧会话,补发会话时再次核对新密码版本。
	if err := s.startSession(w, r, u.ID, string(hash)); err != nil {
		writeErr(w, 500, "会话重建失败,请重新登录")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// dummyHash 用于用户不存在时的等时 bcrypt 比较
const dummyHash = "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"

// startSession 创建会话并下发 Cookie;任何一步失败都返回错误,避免"看似登录成功实则无会话"
func (s *Server) startSession(w http.ResponseWriter, r *http.Request, userID int64, verifiedHash ...string) error {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return fmt.Errorf("生成会话令牌失败: %w", err)
	}
	token := hex.EncodeToString(b)
	hash := ""
	if len(verifiedHash) > 0 {
		hash = verifiedHash[0]
	}
	if err := s.Store.CreateSessionChecked(token, userID, time.Duration(s.Cfg.Session.Days)*24*time.Hour, hash); err != nil {
		return fmt.Errorf("写入会话失败: %w", err)
	}
	s.setSessionCookie(w, r, token)
	return nil
}

// setSessionCookie 下发/续期会话 Cookie(滑动续期时复用同一 token)
func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	days := s.Cfg.Session.Days
	if days <= 0 {
		days = 30
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/",
		MaxAge:   days * 86400,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.requestIsHTTPS(r),
	})
}

// requestIsHTTPS 判断请求是否为 HTTPS。
// X-Forwarded-Proto 仅在对端是可信代理时才采信,否则可被客户端随意伪造。
func (s *Server) requestIsHTTPS(r *http.Request) bool {
	if r == nil {
		return false
	}
	if r.TLS != nil {
		return true
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil || !s.isTrustedProxy(ip) {
		return false
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}
