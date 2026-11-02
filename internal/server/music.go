package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"mime"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"tbmusic/internal/config"
	"tbmusic/internal/lxsource"
	"tbmusic/internal/music"
	"tbmusic/internal/store"
)

// cacheSongKey 将完整歌曲输入纳入缓存键，避免脚本依赖字段不同的请求共享结果。
func cacheSongKey(song *music.Song) string {
	return requestKey("song-v2", song)
}

func (s *Server) source(id string) (music.Source, bool) {
	src, ok := s.Sources[id]
	return src, ok
}

/* ---------- /api/sources ---------- */

type sourceInfo struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	SearchTypes []string `json:"searchTypes"`
	Qualitys    []string `json:"qualitys"`
	Ready       bool     `json:"ready"`
}

func (s *Server) handleSources(w http.ResponseWriter, r *http.Request, u *store.User) {
	// 展示名以配置为准:后台改名后立即生效,不必重载脚本
	nameByURL := make(map[string]string, 24)
	for _, sc := range s.Cfg.ActiveScripts() {
		nameByURL[sc.URL] = sc.Name
	}
	displayName := func(m *lxsource.Manager) string {
		if n, ok := nameByURL[m.URL]; ok && n != "" {
			return n
		}
		return m.Name
	}

	scripts := make([]map[string]any, 0, 8)
	for _, m := range s.LX.All() {
		info := m.GetInfo()
		item := map[string]any{
			"name":    displayName(m),
			"version": info.Version,
			"author":  info.Author,
			"ready":   m.Ready(),
		}
		if err := m.GetInitErr(); err != nil {
			// 初始化错误原文可能包含内部主机名、上游响应片段等细节,只给管理员看
			if u != nil && u.IsAdmin {
				item["error"] = err.Error()
			} else {
				item["error"] = "音源暂不可用"
			}
		}
		if alert := m.GetUpdateAlert(); alert != "" {
			item["updateAlert"] = alert
		}
		scripts = append(scripts, item)
	}

	// script 字段保留给旧前端:取主音源(第一个脚本)的信息
	script := map[string]any{}
	if primary := s.LX.Primary(); primary != nil {
		info := primary.GetInfo()
		script["name"] = info.Name
		script["version"] = info.Version
		script["author"] = info.Author
		script["status"] = "ok"
		if err := primary.GetInitErr(); err != nil {
			script["status"] = "error"
			// 同上:错误细节仅管理员可见
			if u != nil && u.IsAdmin {
				script["error"] = err.Error()
			} else {
				script["error"] = "音源暂不可用"
			}
		}
		if alert := primary.GetUpdateAlert(); alert != "" {
			script["updateAlert"] = alert
		}
	} else if len(scripts) == 0 {
		script["status"] = "loading"
	} else {
		script["status"] = "error"
		script["error"] = "没有可用的采集脚本"
	}

	out := map[string]*sourceInfo{}
	for id, src := range s.Sources {
		si := &sourceInfo{ID: id, Name: src.Name(), SearchTypes: src.SearchTypes(), Qualitys: []string{}}
		if si.SearchTypes == nil {
			si.SearchTypes = []string{}
		}
		out[id] = si
	}
	// 音源能力取并集:任一就绪脚本声明支持即可
	for _, m := range s.LX.All() {
		if !m.Ready() {
			continue
		}
		for id, meta := range m.GetSources() {
			if si, ok := out[id]; ok {
				if len(si.Qualitys) == 0 && len(meta.Qualitys) > 0 {
					si.Qualitys = meta.Qualitys
				}
				si.Ready = true
			}
		}
	}
	writeJSON(w, 200, map[string]any{"script": script, "scripts": scripts, "sources": out})
}

/* ---------- /api/search ---------- */

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request, u *store.User) {
	q := r.URL.Query()
	srcID := q.Get("source")
	src, ok := s.source(srcID)
	if !ok {
		writeErr(w, 400, "未知音源: "+srcID)
		return
	}
	typ := q.Get("type")
	if typ == "" {
		typ = "music"
	}
	keyword := strings.TrimSpace(q.Get("keyword"))
	if len([]rune(keyword)) > 100 {
		writeErr(w, 400, "关键词过长")
		return
	}
	if keyword == "" {
		writeErr(w, 400, "关键词不能为空")
		return
	}
	page, limit := clampPaging(q.Get("page"), q.Get("limit"))

	key := requestKey("search", srcID, typ, keyword, page, limit)
	s.cachedJSON(w, r, key, s.Cfg.Cache.SearchTTL, func() (any, error) {
		switch typ {
		case "music":
			return pageResult(src.SearchMusic(r.Context(), keyword, page, limit))
		case "album":
			return pageResult(src.SearchAlbum(r.Context(), keyword, page, limit))
		case "songlist":
			return pageResult(src.SearchSonglist(r.Context(), keyword, page, limit))
		case "artist":
			return pageResult(src.SearchArtist(r.Context(), keyword, page, limit))
		case "lyric":
			return pageResult(src.SearchLyric(r.Context(), keyword, page, limit))
		default:
			return nil, music.ErrUnsupported
		}
	})
}

func typeName(t string) string {
	switch t {
	case "music":
		return "歌曲"
	case "album":
		return "专辑"
	case "songlist":
		return "歌单"
	case "artist":
		return "歌手"
	case "lyric":
		return "歌词"
	}
	return t
}

func replyPage[T any](w http.ResponseWriter, p *music.Page[T], err error) {
	if err != nil {
		if errors.Is(err, music.ErrUnsupported) {
			writeErr(w, 400, err.Error())
			return
		}
		// 上游错误可能带响应体片段,只进服务端日志,不回传客户端
		log.Printf("[music] 上游请求失败: %v", err)
		writeErr(w, 502, "上游接口暂时不可用,请稍后重试")
		return
	}
	if p == nil {
		p = &music.Page[T]{}
	}
	if p.List == nil {
		p.List = []T{}
	}
	writeJSON(w, 200, p)
}

func clampPaging(pageStr, limitStr string) (int, int) {
	page, limit := 1, 30
	if n := atoiDefault(pageStr, 1); n > 0 {
		page = n
	}
	if n := atoiDefault(limitStr, 30); n > 0 {
		limit = n
	}
	if limit < 10 {
		limit = 10
	}
	if limit > 100 {
		limit = 100 // 与前端详情页 limit=100 对齐,避免大歌单被静默截断
	}
	if page > 100 {
		page = 100
	}
	return page, limit
}

func atoiDefault(s string, def int) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return def
		}
		n = n*10 + int(c-'0')
		if n > 100000 {
			return def
		}
	}
	if s == "" {
		return def
	}
	return n
}

/* ---------- /api/detail ---------- */

func (s *Server) handleDetail(w http.ResponseWriter, r *http.Request, u *store.User) {
	q := r.URL.Query()
	srcID := q.Get("source")
	src, ok := s.source(srcID)
	if !ok {
		writeErr(w, 400, "未知音源: "+srcID)
		return
	}
	id := q.Get("id")
	if id == "" {
		writeErr(w, 400, "缺少 id")
		return
	}
	page, limit := clampPaging(q.Get("page"), q.Get("limit"))
	typ := q.Get("type")
	if typ != "album" && typ != "songlist" && typ != "artist" {
		writeErr(w, 400, "type 需为 album/songlist/artist")
		return
	}
	if len(id) > 256 {
		writeErr(w, 400, "id 过长")
		return
	}
	key := requestKey("detail", srcID, typ, id, page, limit)
	s.cachedJSON(w, r, key, s.Cfg.Cache.DetailTTL, func() (any, error) {
		switch typ {
		case "album":
			return pageResult(src.AlbumSongs(r.Context(), id, page, limit))
		case "songlist":
			return pageResult(src.SonglistSongs(r.Context(), id, page, limit))
		default:
			return pageResult(src.ArtistSongs(r.Context(), id, page, limit))
		}
	})
}

/* ---------- /api/music-url ---------- */

type musicReq struct {
	Source    string     `json:"source"`
	Quality   string     `json:"quality"`
	MusicInfo music.Song `json:"musicInfo"`
}

// 音质优先级(高→低),用于自动降级
var qualityRank = []string{"flac24bit", "hires", "master", "atmos", "flac", "320k", "192k", "128k"}

func rankOf(q string) int {
	for i, x := range qualityRank {
		if x == q {
			return i
		}
	}
	return len(qualityRank)
}

func (s *Server) handleMusicURL(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req musicReq
	if err := decodeBody(w, r, &req); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	if _, ok := s.source(req.Source); !ok {
		writeErr(w, 400, "未知音源: "+req.Source)
		return
	}
	// 用户级限流:正常切歌(含降级重试)约 2~3 次/分钟,120 足够宽松,
	// 只拦异常刷接口;触发时前端不会自动跳歌。
	if !limiter.allow("url:"+strconv.FormatInt(u.ID, 10), 120, time.Minute) {
		writeErr(w, http.StatusTooManyRequests, "取链请求过于频繁,请稍后再试")
		return
	}
	if req.Quality == "" {
		req.Quality = "320k"
	}

	// 整体预算:前端 30s 超时,这里留 5s 余量;多脚本回退不会无限拖长
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()

	songMap := map[string]any{}
	b, _ := json.Marshal(req.MusicInfo)
	json.Unmarshal(b, &songMap)

	cacheKey := requestKey("url", req.Source, cacheSongKey(&req.MusicInfo), req.Quality)
	generation := s.Cache.Generation()
	reply := func(playURL, quality, script string, cached bool) {
		result := resolvedURL{URL: playURL, Quality: quality, Script: script}
		if !cached {
			b, _ := json.Marshal(result)
			if err := s.Cache.SetIfGeneration(cacheKey, b, s.Cfg.Cache.UrlTTL, requestKey("url-tag", playURL), generation); err != nil {
				log.Printf("[cache] 播放地址缓存失败: %v", err)
			}
		}
		writeJSON(w, 200, map[string]any{"url": playURL, "quality": quality, "script": script, "cached": cached,
			"stream": "/api/stream?source=" + url.QueryEscape(req.Source) + "&url=" + url.QueryEscape(playURL)})
	}
	if b, ok := s.Cache.Get(cacheKey); ok {
		var result resolvedURL
		if json.Unmarshal(b, &result) == nil && s.allowedPlayURL(result.URL) {
			reply(result.URL, result.Quality, result.Script, true)
			return
		}
		s.Cache.Delete(cacheKey)
	}
	if s.LX == nil || s.LX.ReadyCount() == 0 {
		writeErr(w, 503, "采集脚本未就绪,请稍后重试或检查设置")
		return
	}

	// 取链结果必须先过白名单:部分脚本返回的是"二次跳转 API"而不是音频直链,
	// 直接透传给 /api/stream 只会 403。这类结果视为该脚本失败,继续试下一个。
	hosts := s.Cfg.GetStreamHosts()
	blockedHost := ""
	accept := func(u string) bool {
		p, err := url.Parse(u)
		if err != nil || p.Hostname() == "" || (p.Scheme != "http" && p.Scheme != "https") || p.User != nil {
			return false
		}
		if !config.StreamHostAllowed(p.Hostname(), hosts) {
			if blockedHost == "" {
				blockedHost = p.Hostname()
			}
			return false
		}
		return true
	}

	// 1) 主音源:按音质降级链依次尝试
	if primary := s.LX.PrimaryReady(); primary != nil {
		for _, q := range s.qualityChain(req.Source, req.Quality) {
			if ctxExpired(ctx) {
				break
			}
			playURL, err := primary.MusicURL(ctx, req.Source, q, songMap)
			if err != nil || !accept(playURL) {
				continue
			}
			reply(playURL, q, primary.Name, false)
			return
		}
	}

	// 2) 子音源:随机顺序逐个尝试(不重复),每个只用请求音质试一次
	for _, m := range s.LX.SubsShuffled() {
		if ctxExpired(ctx) {
			break
		}
		playURL, err := m.MusicURL(ctx, req.Source, req.Quality, songMap)
		if err != nil || !accept(playURL) {
			continue
		}
		reply(playURL, req.Quality, m.Name, false)
		return
	}

	if blockedHost != "" {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": "音源返回的域名未在音频白名单中(" + blockedHost + "),请在「系统设置 - 音频域名白名单」中添加",
			"code":  "host_blocked",
		})
		return
	}
	writeJSON(w, http.StatusNotFound, map[string]string{
		"error": "当前歌曲没有可用资源",
		"code":  "no_source",
	})
}

// ctxExpired 判断 ctx 是否已结束
func ctxExpired(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}

// containsStr 字符串切片是否包含
func containsStr(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// qualityChain 生成音质降级链:最多 3 档(请求档 → 更低一档 → 128k),
// 避免与多脚本回退叠加造成组合爆炸
func (s *Server) qualityChain(sourceID, requested string) []string {
	out := []string{requested}
	var supported []string
	for _, m := range s.LX.All() {
		if meta, ok := m.GetSources()[sourceID]; ok && len(meta.Qualitys) > 0 {
			supported = append([]string{}, meta.Qualitys...)
			break
		}
	}
	if len(supported) == 0 {
		return out
	}
	sort.SliceStable(supported, func(i, j int) bool { return rankOf(supported[i]) < rankOf(supported[j]) })
	reqRank := rankOf(requested)
	for _, q := range supported {
		if q != requested && rankOf(q) > reqRank {
			out = append(out, q)
			break
		}
	}
	if !containsStr(out, "128k") && reqRank < rankOf("128k") {
		out = append(out, "128k")
	}
	if len(out) > 3 {
		out = out[:3]
	}
	return out
}

/* ---------- /api/lyric & /api/pic ---------- */

func (s *Server) handleLyric(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req musicReq
	if err := decodeBody(w, r, &req); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	src, ok := s.source(req.Source)
	if !ok {
		writeErr(w, 400, "未知音源: "+req.Source)
		return
	}
	key := requestKey("lyric", req.Source, cacheSongKey(&req.MusicInfo), req.MusicInfo.LrcURL)
	s.cachedJSON(w, r, key, s.Cfg.Cache.LyricTTL, func() (any, error) {
		lrc, err := src.GetLyric(r.Context(), &req.MusicInfo)
		if errors.Is(err, music.ErrUnsupported) {
			return &music.Lyrics{}, nil
		}
		if err != nil {
			return nil, err
		}
		if lrc == nil {
			lrc = &music.Lyrics{}
		}
		return lrc, nil
	})
}

func (s *Server) handlePic(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req musicReq
	if err := decodeBody(w, r, &req); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	src, ok := s.source(req.Source)
	if !ok {
		writeErr(w, 400, "未知音源: "+req.Source)
		return
	}
	// Include every caller-supplied input and change the namespace so previously
	// poisoned entries cannot survive an upgrade.
	key := requestKey("pic-v2", req.Source, req.MusicInfo)
	s.cachedJSON(w, r, key, s.Cfg.Cache.CoverTTL, func() (any, error) {
		pic, err := src.GetPic(r.Context(), &req.MusicInfo)
		if err != nil {
			return nil, err
		}
		if pic == "" {
			return nil, music.ErrUnsupported
		}
		return map[string]string{"url": pic}, nil
	})
}

/* ---------- /api/stream 音频代理 ---------- */

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request, u *store.User) {
	rawURL := r.URL.Query().Get("url")
	if rawURL == "" {
		writeErr(w, 400, "缺少 url")
		return
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		writeErr(w, 400, "url 非法")
		return
	}
	// 域名白名单:先做零成本的字符串判断,未命中直接拒绝,不发任何网络请求。
	// 这一层也是防「本站被当作开放代理」的关键闸门。
	if !config.StreamHostAllowed(parsed.Hostname(), s.Cfg.GetStreamHosts()) {
		writeErr(w, http.StatusForbidden, "该音频域名不在服务器白名单中,请联系管理员在「系统设置 - 音频域名白名单」添加")
		return
	}
	if err := music.SafeOutboundURLContext(r.Context(), rawURL); err != nil {
		writeErr(w, 400, "url 非法: "+err.Error())
		return
	}
	// 并发闸门:每路音频占 1 个客户端连接 + 1 个上游连接,不限并发会被轻易打满
	select {
	case s.streamSem <- struct{}{}:
		defer func() { <-s.streamSem }()
	default:
		writeErr(w, http.StatusServiceUnavailable, "音频通道繁忙,请稍后重试")
		return
	}
	sourceID := r.URL.Query().Get("source")

	upReq, err := http.NewRequestWithContext(r.Context(), http.MethodGet, rawURL, nil)
	if err != nil {
		writeErr(w, 400, "url 非法")
		return
	}
	upReq.Header.Set("User-Agent", music.DefaultUA)
	for k, v := range music.StreamHeaders(sourceID) {
		upReq.Header.Set(k, v)
	}
	if rg := r.Header.Get("Range"); rg != "" {
		upReq.Header.Set("Range", rg)
	}

	client := *music.SafeHTTPClient
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if !config.StreamHostAllowed(next.URL.Hostname(), s.Cfg.GetStreamHosts()) {
			return fmt.Errorf("重定向目标不在音频域名白名单中")
		}
		if len(via) > 5 {
			return fmt.Errorf("重定向过多")
		}
		return music.SafeOutboundURLContext(next.Context(), next.URL.String())
	}
	resp, err := client.Do(upReq)
	if err != nil {
		writeErr(w, 502, "音频源请求失败")
		return
	}
	defer resp.Body.Close()

	// 上游明确报错说明直链已失效(CDN 签名过期等):剔除缓存,下次请求会重新解析,
	// 否则同一首歌会在整个缓存周期内反复拿到同一个坏地址。
	if resp.StatusCode >= 400 {
		s.Cache.DeleteTag(requestKey("url-tag", rawURL))
		writeErr(w, http.StatusBadGateway, fmt.Sprintf("音频源已失效(%d),请重试", resp.StatusCode))
		return
	}

	for _, h := range []string{"Content-Range", "Accept-Ranges", "Etag", "Last-Modified"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	// Content-Length 只按 Transport 解析出的实际长度回传:
	// 直接透传上游头会在"压缩后自动解压/分块传输"时与实际写出的字节数不符,
	// 客户端会按声明长度空等,或被判定为响应损坏。-1 表示长度未知,交给 chunked。
	expected := resp.ContentLength
	if expected >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(expected, 10))
	}
	// 仅透传音频类 Content-Type;其余一律 octet-stream,防止上游返回 text/html 造成同源 XSS
	ct := resp.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "audio/") || strings.HasPrefix(ct, "application/ogg") || strings.HasPrefix(ct, "video/") {
		w.Header().Set("Content-Type", ct)
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	if name := r.URL.Query().Get("download"); name != "" {
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{
			"filename": downloadFilename(name, rawURL, ct),
		}))
	}
	w.WriteHeader(resp.StatusCode)

	controller := http.NewResponseController(w)
	defer controller.SetWriteDeadline(time.Time{})
	// 上游读空闲超时:超过 streamIdleTimeout 没有新字节就主动断开,
	// 防止上游半死连接长期占住 goroutine 与连接(客户端断开则由 r.Context() 兜底)
	idle := time.AfterFunc(streamIdleTimeout, func() { resp.Body.Close() })
	defer idle.Stop()
	body := &idleResetReader{r: resp.Body, t: idle}
	written, copyErr := io.CopyBuffer(flushWriter{w: w, controller: controller}, body, make([]byte, 64<<10))
	if expected >= 0 && written != expected {
		// 上游提前断流(空闲超时 / 连接被重置):状态码与 Content-Length 都已发出,
		// 无法再改响应,只能保证连接被关闭并留下可排查的日志 ——
		// Go 对"未写满声明长度"的响应会自动关闭连接,客户端据此判定传输中断,
		// 而不是把截断的音频当成完整文件。
		log.Printf("[stream] 上游提前结束 source=%s 期望=%d 实际=%d err=%v", sourceID, expected, written, copyErr)
	}
}

// 清理文件名并使用音频扩展名,Content-Disposition 支持中文原生下载。
func downloadFilename(name, rawURL, contentType string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune(`\/:*?"<>|`, r) {
			return '_'
		}
		return r
	}, name)
	name = strings.Trim(strings.TrimSpace(clipStr(name, 80)), ". ")
	if name == "" {
		name = "未命名"
	}
	ext := ""
	if u, err := url.Parse(rawURL); err == nil {
		ext = strings.ToLower(path.Ext(u.Path))
	}
	switch ext {
	case ".mp3", ".flac", ".m4a", ".aac", ".ogg", ".wav", ".opus":
	default:
		mediaType, _, _ := mime.ParseMediaType(contentType)
		ext = map[string]string{
			"audio/flac": ".flac", "audio/x-flac": ".flac",
			"audio/mp4": ".m4a", "audio/x-m4a": ".m4a", "video/mp4": ".m4a",
			"audio/aac": ".aac", "audio/ogg": ".ogg", "application/ogg": ".ogg",
			"audio/wav": ".wav", "audio/x-wav": ".wav", "audio/opus": ".opus",
		}[mediaType]
		if ext == "" {
			ext = ".mp3"
		}
	}
	return name + ext
}

const streamIdleTimeout = 60 * time.Second

// idleResetReader 每次成功读取都重置空闲计时器
type idleResetReader struct {
	r io.Reader
	t *time.Timer
}

func (c *idleResetReader) Read(p []byte) (int, error) {
	c.t.Reset(streamIdleTimeout)
	return c.r.Read(p)
}

// flushWriter 每写一块即 flush,保证浏览器边下边播
type flushWriter struct {
	w          io.Writer
	controller *http.ResponseController
}

func (fw flushWriter) Write(p []byte) (int, error) {
	if err := fw.controller.SetWriteDeadline(time.Now().Add(streamIdleTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return 0, err
	}
	n, err := fw.w.Write(p)
	if err == nil {
		if flushErr := fw.controller.Flush(); flushErr != nil && !errors.Is(flushErr, http.ErrNotSupported) {
			err = flushErr
		}
	}
	return n, err
}

/* ---------- /api/random-song(s) ---------- */

func (s *Server) handleRandomSong(w http.ResponseWriter, r *http.Request, u *store.User) {
	if song := s.randomSong(r.Context()); song != nil {
		writeJSON(w, 200, map[string]any{"song": song})
		return
	}
	writeErr(w, 502, "随机歌曲获取失败,请重试")
}

// randomKeywords 随机歌单关键词池
var randomKeywords = []string{"热门", "经典", "抖音", "华语", "流行", "伤感", "轻音乐", "民谣", "摇滚", "粤语", "治愈", "夜听", "翻唱", "纯音乐", "现场", "神曲", "回忆", "恋爱", "励志", "情歌", "金曲", "抒情", "慢歌", "热歌"}

// handleRandomSongs 生成一批随机歌曲(多平台随机歌单)
func (s *Server) handleRandomSongs(w http.ResponseWriter, r *http.Request, u *store.User) {
	count := atoiDefault(r.URL.Query().Get("count"), 100)
	if count < 10 {
		count = 10
	}
	if count > 150 {
		count = 150
	}
	// 单次请求最多触发数十次上游搜索,必须限流,否则可被用于放大打上游
	if !limiter.allow("rand:"+strconv.FormatInt(u.ID, 10), 6, time.Minute) {
		writeErr(w, http.StatusTooManyRequests, "生成过于频繁,请稍后再试")
		return
	}
	// 整体预算:到期后不再发起新的上游请求,保证请求时长可控
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	songs := s.randomSongsMulti(ctx, count)
	if len(songs) == 0 {
		writeErr(w, 502, "随机歌单生成失败,请重试")
		return
	}
	writeJSON(w, 200, map[string]any{"list": songs, "count": len(songs)})
}

// searchWithCtx 将整体时间预算传递到音源的每一次 HTTP 请求。
func (s *Server) searchWithCtx(ctx context.Context, src music.Source, keyword string, page, limit int) (*music.Page[music.Song], error) {
	b, err := s.cached(ctx, requestKey("search", src.ID(), "music", keyword, page, limit), s.Cfg.Cache.SearchTTL, func() ([]byte, error) {
		p, err := pageResult(src.SearchMusic(ctx, keyword, page, limit))
		if err != nil {
			return nil, err
		}
		return json.Marshal(p)
	})
	if err != nil {
		return nil, err
	}
	var p music.Page[music.Song]
	err = json.Unmarshal(b, &p)
	return &p, err
}

// randomSongsMulti 多平台随机:各音源随机关键词+随机页轮转凑数,失败源跳过,酷我榜单池兜底
func (s *Server) randomSongsMulti(ctx context.Context, count int) []music.Song {
	ids := make([]string, 0, len(s.Sources))
	for id := range s.Sources {
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil
	}
	rand.Shuffle(len(ids), func(i, j int) { ids[i], ids[j] = ids[j], ids[i] })

	seen := map[string]bool{}
	out := make([]music.Song, 0, count)
	// 每源单轮最多贡献数:保证多源混合
	perSource := count/len(ids) + 10

	for round := 0; round < 3 && len(out) < count; round++ {
		for _, id := range ids {
			if len(out) >= count || ctxExpired(ctx) {
				break
			}
			src, ok := s.source(id)
			if !ok {
				continue
			}
			kw := randomKeywords[rand.Intn(len(randomKeywords))]
			page := rand.Intn(6) + 1
			p, err := s.searchWithCtx(ctx, src, kw, page, 30)
			if err != nil || p == nil || len(p.List) == 0 {
				continue
			}
			list := p.List
			rand.Shuffle(len(list), func(i, j int) { list[i], list[j] = list[j], list[i] })
			take := perSource
			if take > len(list) {
				take = len(list)
			}
			for i := 0; i < take && len(out) < count; i++ {
				if seen[list[i].Key()] {
					continue
				}
				seen[list[i].Key()] = true
				out = append(out, list[i])
			}
		}
	}
	// 酷我榜单池兜底补齐(同样受整体时间预算约束,避免长时间串行打上游)
	for len(out) < count {
		if ctxExpired(ctx) {
			break
		}
		sg := s.randomSong(ctx)
		if sg == nil || seen[sg.Key()] {
			break
		}
		seen[sg.Key()] = true
		out = append(out, *sg)
	}
	// 跨源打散
	rand.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

func (s *Server) randomSong(ctx context.Context) *music.Song {
	if src, ok := s.source("kw"); ok {
		if rs, ok := src.(interface {
			RandomSong(context.Context) (*music.Song, error)
		}); ok {
			song, err := rs.RandomSong(ctx)
			if err == nil && song != nil {
				return song
			}
		}
	}
	return nil
}

/* ---------- /api/toplists 热门榜单 ---------- */

func (s *Server) handleToplists(w http.ResponseWriter, r *http.Request, u *store.User) {
	s.cachedJSON(w, r, requestKey("detail", "toplists", "wy"), s.Cfg.Cache.DetailTTL, func() (any, error) {
		if src, ok := s.source("wy"); ok {
			if ts, ok := src.(interface {
				Toplists(context.Context) ([]music.Toplist, error)
			}); ok {
				list, err := ts.Toplists(r.Context())
				if err != nil {
					return nil, err
				}
				if list == nil {
					list = []music.Toplist{}
				}
				return map[string]any{"list": list}, nil
			}
		}
		return nil, music.ErrUnsupported
	})
}

// ReloadScripts 重新加载全部采集脚本(失败保留旧实例);并发调用由互斥锁串行化
func (s *Server) ReloadScripts() {
	s.reloadMu.Lock()
	defer s.reloadMu.Unlock()

	scripts := s.Cfg.ActiveScripts()
	if len(scripts) == 0 {
		s.LX.Set(nil)
		log.Println("[采集接口] 未配置脚本")
		return
	}
	timeout := s.Cfg.SourceTimeout()

	// 并行加载,按下标回填以保证"配置顺序 = 优先级"
	managers := make([]*lxsource.Manager, len(scripts))
	var wg sync.WaitGroup
	for i, sc := range scripts {
		wg.Add(1)
		go func(i int, sc config.Script) {
			defer wg.Done()
			m := lxsource.NewManager(sc.Name, sc.URL, timeout)
			if err := m.Load(); err != nil {
				// 失败的实例同样入池(Ready() 为 false,不参与取链):
				// 这样池顺序与配置严格一致,后台能展示失败原因,
				// 重试加载时可原位替换而不是被挤到队尾
				log.Printf("[采集接口] %s 加载失败: %v", sc.Name, err)
			}
			managers[i] = m
		}(i, sc)
	}
	wg.Wait()

	list := make([]*lxsource.Manager, 0, len(managers))
	ready := 0
	for _, m := range managers {
		if m == nil {
			continue
		}
		list = append(list, m)
		if m.Ready() {
			ready++
		}
	}
	s.LX.SetPreservingReady(list)
	ready = s.LX.ReadyCount()
	log.Printf("[采集接口] 已加载 %d/%d 个脚本", ready, len(scripts))
}
