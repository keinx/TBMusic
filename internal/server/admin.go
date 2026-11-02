package server

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"tbmusic/internal/config"
	"tbmusic/internal/lxsource"
	"tbmusic/internal/store"
)

/* ---------- 用户管理(超管) ---------- */

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request, u *store.User) {
	users, err := s.Store.ListUsers()
	if err != nil {
		log.Printf("[admin] 查询用户列表失败: %v", err)
		writeErr(w, 500, "查询用户列表失败")
		return
	}
	if users == nil {
		users = []store.UserWithStats{}
	}
	writeJSON(w, 200, map[string]any{"list": users})
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, 400, "id 非法")
		return
	}
	if id == u.ID {
		writeErr(w, 400, "不能删除当前登录账户")
		return
	}
	if err := s.Store.DeleteUserCascade(id); err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			writeErr(w, 404, "用户不存在")
		case errors.Is(err, store.ErrProtectedUser):
			writeErr(w, 400, err.Error())
		default:
			log.Printf("[admin] 删除用户失败 id=%d: %v", id, err)
			writeErr(w, 500, "删除失败,请稍后重试")
		}
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// handleResetUserPassword 超管重置用户密码
func (s *Server) handleResetUserPassword(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, 400, "id 非法")
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := decodeBody(w, r, &req); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	if !validPassword(req.Password) {
		writeErr(w, 400, "密码长度需为 6-64 位")
		return
	}
	// 限流:bcrypt 哈希是 CPU 密集型操作
	if !limiter.allow("admin-pw:"+strconv.FormatInt(u.ID, 10), 20, time.Minute) {
		writeErr(w, 429, "操作过于频繁,请稍后再试")
		return
	}
	// 先确认目标用户存在且允许重置,避免为必然失败的请求白付一次 bcrypt
	target, err := s.Store.GetUserByID(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, 404, "用户不存在")
		} else {
			log.Printf("[admin] 查询用户失败 id=%d: %v", id, err)
			writeErr(w, 500, "服务暂不可用,请稍后再试")
		}
		return
	}
	if target.IsAdmin || strings.EqualFold(target.Username, "admin") {
		writeErr(w, 400, store.ErrAdminPasswordReset.Error())
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		writeErr(w, 500, "系统错误")
		return
	}
	if err := s.Store.ResetUserPassword(id, string(hash)); err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			writeErr(w, 404, "用户不存在")
		case errors.Is(err, store.ErrAdminPasswordReset):
			writeErr(w, 400, err.Error())
		default:
			log.Printf("[admin] 重置密码失败 id=%d: %v", id, err)
			writeErr(w, 500, "重置失败,请稍后重试")
		}
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// handleUpdateUserRemark 超管为用户设置备注
func (s *Server) handleUpdateUserRemark(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, 400, "id 非法")
		return
	}
	var req struct {
		Remark string `json:"remark"`
	}
	if err := decodeBody(w, r, &req); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	remark := strings.TrimSpace(req.Remark)
	if len([]rune(remark)) > 30 {
		writeErr(w, 400, "备注最多 30 字")
		return
	}
	if err := s.Store.UpdateUserRemark(id, remark); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, 404, "用户不存在")
		} else {
			log.Printf("[admin] 更新备注失败 id=%d: %v", id, err)
			writeErr(w, 500, "保存失败,请稍后重试")
		}
		return
	}
	writeJSON(w, 200, map[string]string{"remark": remark})
}

/* ---------- 系统设置(超管):采集接口 + 注册开关 ---------- */

// maxScripts 采集脚本数量上限(每个脚本一个 JS 运行时,过多会吃内存)
const maxScripts = lxsource.MaxManagers

type adminSettings struct {
	Scripts        []adminScript `json:"scripts"`
	AllowRegister  *bool         `json:"allowRegister"`
	InviteRequired *bool         `json:"inviteRequired"`
	InviteCode     *string       `json:"inviteCode"`
	StreamHosts    []string      `json:"streamHosts"`
}

// adminScript 后台展示用:配置项 + 运行时状态
type adminScript struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	Ready   bool   `json:"ready"`
	Version string `json:"version,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (s *Server) handleGetAdminSettings(w http.ResponseWriter, r *http.Request, u *store.User) {
	allowReg, inviteReq, inviteCode := s.Cfg.GetAuthSettings()
	out := adminSettings{
		AllowRegister:  &allowReg,
		InviteRequired: &inviteReq,
		InviteCode:     &inviteCode,
		StreamHosts:    s.Cfg.GetStreamHosts(),
	}
	if out.StreamHosts == nil {
		out.StreamHosts = []string{}
	}

	// 以配置为权威来源(加载失败的脚本也要能看到并删除),再匹配运行时状态
	running := map[string]*lxsource.Manager{}
	for _, m := range s.LX.All() {
		running[m.URL] = m
	}
	out.Scripts = make([]adminScript, 0, len(out.Scripts))
	for _, sc := range s.Cfg.ActiveScripts() {
		item := adminScript{Name: sc.Name, URL: sc.URL}
		if m, ok := running[sc.URL]; ok {
			item.Ready = m.Ready()
			item.Version = m.GetInfo().Version
			if err := m.GetInitErr(); err != nil {
				item.Error = err.Error()
			} else if !item.Ready {
				item.Error = "正在初始化"
			}
		} else {
			item.Error = "未加载(加载失败或正在重载)"
		}
		out.Scripts = append(out.Scripts, item)
	}
	writeJSON(w, 200, out)
}

// handleRetryScript 重新加载单个采集脚本。
// 脚本下载 + 初始化最长可达数十秒,不能占住这个 HTTP 请求,
// 因此立即返回并在后台加载,前端稍后刷新状态即可。
func (s *Server) handleRetryScript(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req struct {
		URL string `json:"url"`
	}
	if err := decodeBody(w, r, &req); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	rawURL := strings.TrimSpace(req.URL)
	if rawURL == "" {
		writeErr(w, 400, "缺少脚本地址")
		return
	}
	name := ""
	found := false
	for _, sc := range s.Cfg.ActiveScripts() {
		if sc.URL == rawURL {
			name, found = sc.Name, true
			break
		}
	}
	if !found {
		writeErr(w, 404, "该脚本不在配置中,请先保存设置")
		return
	}
	timeout := s.Cfg.SourceTimeout()
	go func() {
		if err := s.LX.Retry(name, rawURL, timeout); err != nil {
			log.Printf("[采集接口] %s 重试加载失败: %v", name, err)
			return
		}
		log.Printf("[采集接口] %s 重试加载成功", name)
	}()
	writeJSON(w, 200, map[string]any{"ok": true, "async": true})
}

func (s *Server) handlePutAdminSettings(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req adminSettings
	if err := decodeBody(w, r, &req); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	// 与白名单增删共用同一把锁:两条路径都是"读配置 → 改 → 整体重写 config.yaml",
	// 交错执行会互相覆盖(丢更新);串行化后内存与磁盘才始终一致。
	s.adminMu.Lock()
	defer s.adminMu.Unlock()

	reloadScript := false
	var newScripts []config.Script // 校验全部通过后统一应用;nil 表示不改脚本
	if req.Scripts != nil {
		if len(req.Scripts) > maxScripts {
			writeErr(w, 400, fmt.Sprintf("采集脚本最多 %d 个", maxScripts))
			return
		}
		next := make([]config.Script, 0, len(req.Scripts))
		seen := make(map[string]bool, len(req.Scripts))
		for _, sc := range req.Scripts {
			url := strings.TrimSpace(sc.URL)
			if url == "" {
				continue
			}
			if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
				writeErr(w, 400, "采集接口 URL 需以 http(s) 开头: "+url)
				return
			}
			if seen[url] {
				continue // 静默去重
			}
			seen[url] = true
			name := strings.TrimSpace(sc.Name)
			if name == "" {
				name = guessScriptName(url)
			}
			next = append(next, config.Script{Name: clipRunes(name, 40), URL: url})
		}
		if len(next) == 0 {
			writeErr(w, 400, "至少保留一个采集接口")
			return
		}
		// 只有 URL 序列变化才需要重载脚本;仅改名不必重启音频运行时
		reloadScript = !sameScriptURLs(s.Cfg.ActiveScripts(), next)
		newScripts = next
	}
	// 快照当前值,合并请求中的可选项;全部校验通过后再一次性写入,
	// 避免脚本已生效而后续校验失败导致内存与磁盘配置分叉
	prevAllowReg, prevInviteReq, prevInviteCode := s.Cfg.GetAuthSettings()
	prevScripts := s.Cfg.GetScripts() // 原始列表(含空 URL 项),用于落盘失败时回滚
	allowRegister, inviteRequired := prevAllowReg, prevInviteReq
	if req.AllowRegister != nil {
		allowRegister = *req.AllowRegister
	}
	if req.InviteRequired != nil {
		inviteRequired = *req.InviteRequired
	}
	// 仅当请求体显式携带 inviteCode 字段时才覆盖,避免只改开关时误清邀请码
	inviteCodeValue := prevInviteCode
	if req.InviteCode != nil {
		inviteCodeValue = strings.TrimSpace(*req.InviteCode)
	}
	// 开启邀请码校验时必须已设置非空邀请码
	if inviteRequired && inviteCodeValue == "" {
		writeErr(w, 400, "开启邀请码前请先设置邀请码")
		return
	}
	// 校验全部通过,统一提交(auth 与 scripts 一起生效)
	s.Cfg.SetAuthSettings(allowRegister, inviteRequired, inviteCodeValue)
	if newScripts != nil {
		s.Cfg.SetScripts(newScripts)
	}
	if err := s.Cfg.Save(s.ConfigPath); err != nil {
		// 落盘失败:回滚内存,保证内存 / 磁盘 / 运行时三者一致
		s.Cfg.SetAuthSettings(prevAllowReg, prevInviteReq, prevInviteCode)
		s.Cfg.SetScripts(prevScripts)
		log.Printf("[admin] 写入配置失败: %v", err)
		writeErr(w, 500, "配置写入失败,请检查文件权限")
		return
	}
	if reloadScript {
		go s.ReloadScripts()
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

/* ---------- 采集脚本辅助 ---------- */

// guessScriptName 从句柄 URL 猜测一个可读名字(取末段路径,如 .../main/changqing/latest.js → changqing)
func guessScriptName(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "自定义采集接口"
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i := len(parts) - 1; i >= 0; i-- {
		p := strings.TrimSuffix(parts[i], ".js")
		if p == "" || strings.EqualFold(p, "latest") {
			continue
		}
		return clipRunes(p, 40)
	}
	if u.Host != "" {
		return clipRunes(u.Host, 40)
	}
	return "自定义采集接口"
}

// sameScriptURLs 判断脚本地址序列是否一致(顺序变化意味着优先级变化,需要重载)
func sameScriptURLs(a, b []config.Script) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].URL != b[i].URL {
			return false
		}
	}
	return true
}

// clipRunes 按 rune 截断
func clipRunes(v string, n int) string {
	r := []rune(v)
	if len(r) <= n {
		return v
	}
	return string(r[:n])
}

/* ---------- 音频代理白名单(逐条增删) ---------- */

const maxStreamHosts = 200

// normalizeHostPattern 规范化并校验白名单条目,支持 domain、*.domain、*
func normalizeHostPattern(raw string) (string, error) {
	h := strings.ToLower(strings.TrimSpace(raw))
	h = strings.TrimPrefix(h, "https://")
	h = strings.TrimPrefix(h, "http://")
	if i := strings.IndexAny(h, "/?#"); i >= 0 {
		h = h[:i]
	}
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	h = strings.TrimSuffix(strings.TrimSpace(h), ".")
	if h == "" {
		return "", errors.New("域名不能为空")
	}
	if h == "*" {
		return h, nil // 放开全部(不推荐,但允许管理员显式选择)
	}
	if strings.HasPrefix(h, "*.") {
		if !validDomainPart(h[2:]) {
			return "", errors.New("域名格式不合法")
		}
		return h, nil
	}
	if strings.Contains(h, "*") {
		return "", errors.New("通配符只支持 *.example.com 形式")
	}
	if !validDomainPart(h) {
		return "", errors.New("域名格式不合法,请填写如 api.example.com")
	}
	return h, nil
}

// validDomainPart 校验普通域名(至少两级,仅字母数字与连字符)
func validDomainPart(d string) bool {
	if len(d) < 4 || len(d) > 253 || !strings.Contains(d, ".") {
		return false
	}
	for _, label := range strings.Split(d, ".") {
		if label == "" || len(label) > 63 {
			return false
		}
		for i, c := range label {
			ok := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-'
			if !ok || (c == '-' && (i == 0 || i == len(label)-1)) {
				return false
			}
		}
	}
	return true
}

// handleAddStreamHost 添加一条白名单(单条添加,自动去重)
func (s *Server) handleAddStreamHost(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req struct {
		Host string `json:"host"`
	}
	if err := decodeBody(w, r, &req); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	pattern, err := normalizeHostPattern(req.Host)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	hosts, err := s.mutateStreamHosts(func(cur []string) ([]string, error) {
		for _, x := range cur {
			if strings.EqualFold(x, pattern) {
				return nil, errHostExists
			}
		}
		if len(cur) >= maxStreamHosts {
			return nil, errHostLimit
		}
		return append(cur, pattern), nil
	})
	if err != nil {
		switch {
		case errors.Is(err, errHostExists):
			writeErr(w, 400, "该域名已在白名单中")
		case errors.Is(err, errHostLimit):
			writeErr(w, 400, fmt.Sprintf("白名单最多 %d 条", maxStreamHosts))
		default:
			log.Printf("[admin] 保存白名单失败: %v", err)
			writeErr(w, 500, "保存失败,请检查配置文件权限")
		}
		return
	}
	writeJSON(w, 200, map[string]any{"list": hosts})
}

// handleRemoveStreamHost 删除一条白名单
func (s *Server) handleRemoveStreamHost(w http.ResponseWriter, r *http.Request, u *store.User) {
	pattern := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("host")))
	if pattern == "" {
		writeErr(w, 400, "缺少 host 参数")
		return
	}
	hosts, err := s.mutateStreamHosts(func(cur []string) ([]string, error) {
		out := make([]string, 0, len(cur))
		for _, x := range cur {
			if !strings.EqualFold(x, pattern) {
				out = append(out, x)
			}
		}
		if len(out) == len(cur) {
			return nil, errHostMissing
		}
		return out, nil
	})
	if err != nil {
		switch {
		case errors.Is(err, errHostMissing):
			writeErr(w, 404, "该域名不在白名单中")
		default:
			log.Printf("[admin] 保存白名单失败: %v", err)
			writeErr(w, 500, "保存失败,请检查配置文件权限")
		}
		return
	}
	writeJSON(w, 200, map[string]any{"list": hosts})
}

// handleUpdateStreamHost 编辑一条白名单:原位替换(保持列表顺序),new 与其它条目重复时拒绝
func (s *Server) handleUpdateStreamHost(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req struct {
		Old  string `json:"old"`
		Host string `json:"host"`
	}
	if err := decodeBody(w, r, &req); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	oldPat := strings.ToLower(strings.TrimSpace(req.Old))
	if oldPat == "" {
		writeErr(w, 400, "缺少原域名")
		return
	}
	newPat, err := normalizeHostPattern(req.Host)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if newPat == oldPat {
		writeJSON(w, 200, map[string]any{"list": s.Cfg.GetStreamHosts()})
		return
	}
	hosts, err := s.mutateStreamHosts(func(cur []string) ([]string, error) {
		for _, x := range cur {
			if strings.EqualFold(x, newPat) {
				return nil, errHostExists
			}
		}
		out := make([]string, 0, len(cur))
		replaced := false
		for _, x := range cur {
			if strings.EqualFold(x, oldPat) {
				out = append(out, newPat) // 原地替换,不改变顺序
				replaced = true
				continue
			}
			out = append(out, x)
		}
		if !replaced {
			return nil, errHostMissing
		}
		return out, nil
	})
	if err != nil {
		switch {
		case errors.Is(err, errHostExists):
			writeErr(w, 400, "该域名已在白名单中")
		case errors.Is(err, errHostMissing):
			writeErr(w, 404, "该域名不在白名单中")
		default:
			log.Printf("[admin] 保存白名单失败: %v", err)
			writeErr(w, 500, "保存失败,请检查配置文件权限")
		}
		return
	}
	writeJSON(w, 200, map[string]any{"list": hosts})
}

// mutateStreamHosts 串行执行「读-改-写」,避免并发增删互相覆盖
func (s *Server) mutateStreamHosts(fn func([]string) ([]string, error)) ([]string, error) {
	s.adminMu.Lock()
	defer s.adminMu.Unlock()
	cur := s.Cfg.GetStreamHosts()
	next, err := fn(cur)
	if err != nil {
		return nil, err
	}
	s.Cfg.SetStreamHosts(next)
	if err := s.Cfg.Save(s.ConfigPath); err != nil {
		s.Cfg.SetStreamHosts(cur) // 落盘失败回滚内存,保持内存与磁盘一致
		return nil, err
	}
	return next, nil
}

var (
	errHostExists  = errors.New("host exists")
	errHostMissing = errors.New("host missing")
	errHostLimit   = errors.New("host limit")
)
