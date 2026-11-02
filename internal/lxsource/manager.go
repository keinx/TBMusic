// Package lxsource 实现 LX Music 自定义音源脚本协议宿主:
// 用 goja 在后台事件循环中运行采集脚本,通过 globalThis.lx 与其交互,
// 对外暴露 MusicURL(解析播放地址)能力。
package lxsource

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dop251/goja"
	"github.com/dop251/goja/parser"
	"github.com/dop251/goja_nodejs/eventloop"
	"github.com/dop251/goja_nodejs/require"

	"tbmusic/internal/music"
)

// SourceMeta 脚本 inited 事件上报的音源能力
type SourceMeta struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Qualitys []string `json:"qualitys"`
}

// ScriptInfo 脚本头部注释解析结果
type ScriptInfo struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Version     string `json:"version,omitempty"`
	Author      string `json:"author,omitempty"`
	Homepage    string `json:"homepage,omitempty"`
}

// Manager 单个采集脚本的管理器
type Manager struct {
	Name    string
	URL     string
	Timeout time.Duration

	Info        ScriptInfo `json:"info"`
	Sources     map[string]SourceMeta
	UpdateAlert string
	InitErr     error

	loop        *eventloop.EventLoop
	lifecycleMu sync.Mutex // serializes starting/stopping and clearing VM interrupts
	ctx         context.Context
	cancel      context.CancelFunc
	vm          atomic.Pointer[goja.Runtime] // 事件循环的 VM 句柄(Interrupt 可跨 goroutine 直调,不排队)
	handlers    map[string]goja.Callable
	inited      bool
	ready       chan struct{}
	readyOne    sync.Once
	stopped     atomic.Bool
	// poisoned 超时后事件循环无法确认空闲时置位:此后一律不再进入该 VM,
	// 避免旧任务与下一次调用并发执行(需重新加载脚本才能恢复)
	poisoned atomic.Bool

	mu sync.RWMutex // 保护 Info/Sources/UpdateAlert/InitErr/inited 的跨线程读写

	// 并发控制:waitSem 限制排队数,callSem(容量 1)串行执行。
	// 用 channel 而非 Mutex,是为了等待过程可以响应 context 取消。
	waitSem    chan struct{}
	callSem    chan struct{}
	requestCtx context.Context
	httpSem    chan struct{}
}

// maxQueuedCalls 允许同时排队等待取链的请求数
const maxQueuedCalls = 16

func NewManager(name, url string, timeout time.Duration) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		Name:       name,
		URL:        url,
		Timeout:    timeout,
		ctx:        ctx,
		cancel:     cancel,
		waitSem:    make(chan struct{}, maxQueuedCalls),
		callSem:    make(chan struct{}, 1),
		requestCtx: ctx,
		httpSem:    make(chan struct{}, 4),
	}
}

// runInLoop 在事件循环内执行 fn,并统一清理上一次超时遗留的中断标志。
// 所有进入 VM 的任务都必须走这里,否则一次超时中断会让后续任务(例如脚本的
// HTTP 回调)刚起步就被打断。
func (m *Manager) runInLoop(fn func(vm *goja.Runtime)) bool {
	m.lifecycleMu.Lock()
	loop := m.loop
	stopped := m.stopped.Load()
	m.lifecycleMu.Unlock()
	if loop == nil || stopped {
		return false
	}
	return loop.RunOnLoop(func(vm *goja.Runtime) {
		m.lifecycleMu.Lock()
		if m.stopped.Load() {
			m.lifecycleMu.Unlock()
			return
		}
		vm.ClearInterrupt()
		m.vm.Store(vm)
		m.lifecycleMu.Unlock()
		fn(vm)
	})
}

// interrupt 中断当前正在执行的 JS(goroutine 安全,必须直调)
func (m *Manager) interrupt(reason string) {
	if vm := m.vm.Load(); vm != nil {
		vm.Interrupt(reason)
	}
}

var headerRe = regexp.MustCompile(`(?s)/\*!\s*(.*?)\s*\*/`)
var headerFieldRe = regexp.MustCompile(`@(\w+)\s+([^\r\n]+)`)

func parseScriptHeader(script string) ScriptInfo {
	info := ScriptInfo{}
	m := headerRe.FindStringSubmatch(script)
	if m == nil {
		return info
	}
	for _, f := range headerFieldRe.FindAllStringSubmatch(m[1], -1) {
		v := strings.TrimSpace(f[2])
		switch f[1] {
		case "name":
			info.Name = v
		case "description":
			info.Description = v
		case "version":
			info.Version = v
		case "author":
			info.Author = v
		case "homepage":
			info.Homepage = v
		}
	}
	return info
}

// 脚本本地缓存:GitHub 反代下载很慢(单脚本 10~20s),每次启动都重新下载会让
// 一批脚本栽在超时上。缓存有效期内直接读本地文件,点「重试加载」则强制刷新。
var (
	scriptCacheDir = filepath.Join("data", "scriptcache")
	scriptCacheTTL = 6 * time.Hour
	// scriptFetchSem 限制并发下载数:反代带宽有限,并发过多会各自超时
	scriptFetchSem = make(chan struct{}, 4)
)

func (m *Manager) cachePath() string {
	sum := sha256.Sum256([]byte(m.URL))
	return filepath.Join(scriptCacheDir, hex.EncodeToString(sum[:8])+".js")
}

// readCache 读取本地缓存;fresh 为 false 时忽略有效期(下载失败后的兜底)
func (m *Manager) readCache(fresh bool) (string, bool) {
	p := m.cachePath()
	st, err := os.Stat(p)
	if err != nil {
		return "", false
	}
	if fresh && time.Since(st.ModTime()) > scriptCacheTTL {
		return "", false
	}
	b, err := os.ReadFile(p)
	if err != nil || len(b) == 0 {
		return "", false
	}
	return string(b), true
}

func (m *Manager) writeCache(script string) {
	p := m.cachePath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return
	}
	f, err := os.CreateTemp(filepath.Dir(p), ".script-*.tmp")
	if err != nil {
		return
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	_, err = f.WriteString(script)
	closeErr := f.Close()
	if err == nil && closeErr == nil {
		_ = os.Rename(tmp, p)
	}
}

// download 从网络拉取脚本内容(受并发闸门约束)
func (m *Manager) download() (string, error) {
	select {
	case scriptFetchSem <- struct{}{}:
	case <-m.ctx.Done():
		return "", m.ctx.Err()
	}
	defer func() { <-scriptFetchSem }()

	if err := music.SafeOutboundURLContext(m.ctx, m.URL); err != nil {
		return "", err
	}
	client := scriptHTTPClient
	req, err := http.NewRequestWithContext(m.ctx, http.MethodGet, m.URL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", hostUA)
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("下载采集脚本失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("下载采集脚本失败: HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
	if err != nil {
		return "", err
	}
	if len(b) == 0 {
		return "", errors.New("下载采集脚本失败: 内容为空")
	}
	if len(b) > 8<<20 {
		return "", errors.New("采集脚本超过 8 MiB")
	}
	return string(b), nil
}

// fetchScript 取脚本内容:force=false 优先用新鲜缓存,下载失败则退回过期缓存
func (m *Manager) fetchScript(force bool) (string, bool, error) {
	if !force {
		if s, ok := m.readCache(true); ok {
			return s, false, nil
		}
	}
	s, err := m.download()
	if err == nil {
		return s, true, nil
	}
	if old, ok := m.readCache(false); ok {
		log.Printf("[采集接口] %s 更新失败(%v),改用本地缓存", m.Name, err)
		return old, false, nil
	}
	return "", false, err
}

// Load 下载并初始化脚本;成功后脚本常驻后台事件循环。
// 优先使用本地缓存,仅缓存缺失/过期时才走网络。
func (m *Manager) Load() error { return m.load(false) }

// Reload 强制重新下载并初始化(后台「重试加载」按钮)
func (m *Manager) Reload() error { return m.load(true) }

func (m *Manager) load(force bool) (err error) {
	// 失败原因统一记录到 InitErr:后台设置页要展示
	// (以前只有脚本内部 send(inited,error) 才会写入,下载/运行失败时为 nil)
	defer func() {
		if err != nil {
			m.mu.Lock()
			if m.InitErr == nil {
				m.InitErr = err
			}
			m.mu.Unlock()
		}
	}()

	script, downloaded, err := m.fetchScript(force)
	if err != nil {
		return err
	}
	info := parseScriptHeader(script)
	if info.Name == "" {
		info.Name = m.Name
	}
	// 用一个局部 ready 通道等待初始化完成:持锁等待会与 handleSend(持 m.mu
	// 后 close(m.ready))互相死锁,因此这里只做短临界区的状态初始化。
	ready := make(chan struct{})

	m.mu.Lock()
	if m.Name == "" {
		m.Name = info.Name
	}
	m.Info = info
	m.handlers = map[string]goja.Callable{}
	m.Sources = map[string]SourceMeta{}
	m.ready = ready
	m.inited = false
	m.readyOne = sync.Once{}
	m.poisoned.Store(false) // 重载后恢复可用
	m.mu.Unlock()

	registry := require.NewRegistry(require.WithLoader(func(string) ([]byte, error) {
		return nil, errors.New("音源脚本禁止加载本地文件")
	}), require.WithPathResolver(func(base, path string) string {
		return filepath.Join(base, path) // no filesystem/symlink lookup
	}))
	loop := eventloop.NewEventLoop(eventloop.WithRegistry(registry))
	m.lifecycleMu.Lock()
	if m.stopped.Load() {
		m.lifecycleMu.Unlock()
		return errors.New("采集脚本已停止")
	}
	m.loop = loop
	loop.Start()
	m.lifecycleMu.Unlock()

	var runErr error
	done := make(chan struct{})
	ok := m.runInLoop(func(vm *goja.Runtime) {
		defer close(done)
		defer func() {
			if v := recover(); v != nil {
				runErr = fmt.Errorf("脚本初始化失败: %v", v)
			}
		}()
		vm.SetParserOptions(parser.WithDisableSourceMaps)
		m.registerEnv(vm)
		if _, err := vm.RunScript("lx-source-script.js", script); err != nil {
			runErr = fmt.Errorf("脚本执行失败: %w", err)
		}
	})
	if !ok {
		return errors.New("事件循环已终止")
	}
	select {
	case <-done:
	case <-m.ctx.Done():
		return m.ctx.Err()
	case <-time.After(m.Timeout + 20*time.Second):
		// Stop 会等当前 JS 任务结束;先中断卡死的脚本,否则死循环脚本令 Stop 永久挂起
		m.interrupt("脚本执行超时")
		m.Stop()
		return errors.New("脚本执行超时")
	}
	if runErr != nil {
		m.Stop()
		return runErr
	}
	if initErr := m.GetInitErr(); initErr != nil {
		m.Stop()
		return initErr
	}
	select {
	case <-ready:
		if downloaded {
			m.writeCache(script)
		}
		return nil
	case <-m.ctx.Done():
		return m.ctx.Err()
	case <-time.After(m.Timeout + 20*time.Second):
		// 先中断可能卡死的初始化脚本,否则 Stop 永久挂起
		m.interrupt("脚本初始化超时")
		m.Stop()
		return errors.New("脚本初始化超时:未收到 inited 事件")
	}
}

// Stop 停止事件循环(卸载脚本时调用);可重复调用
func (m *Manager) Stop() {
	m.lifecycleMu.Lock()
	if m.stopped.Swap(true) {
		m.lifecycleMu.Unlock()
		return
	}
	m.cancel()
	m.interrupt("采集脚本已停止")
	loop := m.loop
	m.lifecycleMu.Unlock()
	m.mu.Lock()
	m.inited = false
	m.mu.Unlock()
	if loop != nil {
		loop.Terminate()
	}
}

// Ready 脚本是否已就绪(停止/熔断后恒为 false,避免向异常的事件循环投递任务)
func (m *Manager) Ready() bool {
	if m.poisoned.Load() {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.inited && !m.stopped.Load()
}

// drainLoop 在中断脚本后投递一个空任务作为屏障,确认事件循环已处理完此前任务。
// 返回 false 表示循环已停止或 barrier 超时(此时调用方必须停止向该 VM 投递任务)。
func (m *Manager) drainLoop(timeout time.Duration) bool {
	done := make(chan struct{})
	ok := m.runInLoop(func(vm *goja.Runtime) {
		close(done)
	})
	if !ok {
		return false
	}
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// GetInfo 返回脚本信息快照
func (m *Manager) GetInfo() ScriptInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.Info
}

// GetSources 返回支持的音源快照
func (m *Manager) GetSources() map[string]SourceMeta {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]SourceMeta, len(m.Sources))
	for k, v := range m.Sources {
		out[k] = v
	}
	return out
}

// GetInitErr 返回初始化错误
func (m *Manager) GetInitErr() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.InitErr
}

// GetUpdateAlert 返回脚本更新提示
func (m *Manager) GetUpdateAlert() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.UpdateAlert
}

// handleSend 处理脚本 lx.send(...) 上行事件
func (m *Manager) handleSend(event string, data goja.Value) {
	if m.stopped.Load() {
		return // 脚本已卸载,迟到的事件不再改动状态
	}
	if event != "inited" && event != "updateAlert" {
		return
	}
	// Export may invoke JS getters which call lx.on/send again. Never hold a
	// native mutex while executing user JavaScript.
	b, exportErr := json.Marshal(data.Export())
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped.Load() {
		return
	}
	switch event {
	case "inited":
		var payload struct {
			Status  *bool                 `json:"status"`
			Message string                `json:"message"`
			Sources map[string]SourceMeta `json:"sources"`
		}
		if exportErr != nil {
			m.InitErr = fmt.Errorf("解析 inited 事件失败: %w", exportErr)
		} else if err := json.Unmarshal(b, &payload); err != nil {
			m.InitErr = fmt.Errorf("解析 inited 事件失败: %w", err)
		}
		if m.InitErr == nil && payload.Status != nil && !*payload.Status {
			msg := payload.Message
			if msg == "" {
				msg = "脚本初始化失败(status=false)"
			}
			m.InitErr = errors.New(msg)
		}
		if m.InitErr == nil && len(payload.Sources) > 0 {
			m.Sources = payload.Sources
		}
		m.inited = m.InitErr == nil
		if m.inited {
			m.readyOne.Do(func() { close(m.ready) })
		}
	case "updateAlert":
		var payload struct {
			Log       string `json:"log"`
			UpdateURL string `json:"updateUrl"`
		}
		if exportErr == nil {
			json.Unmarshal(b, &payload)
			m.UpdateAlert = strings.TrimSpace(payload.Log + " " + payload.UpdateURL)
		}
	}
}

type urlResult struct {
	url string
	err string
}

// MusicURL 调用脚本解析播放地址。musicInfo 为 lx 兼容字段(map)。
// ctx 取消时,若仍在排队阶段会立刻返回;一旦任务已投递进事件循环,
// 就必须等它自己结束(最长 Timeout)。超时后会中断脚本并投递屏障任务确认 VM 空闲,
// 只有确认空闲才放行下一次调用;屏障失败则熔断该音源,避免两者并发进入同一 VM。
func (m *Manager) MusicURL(ctx context.Context, source, quality string, musicInfo map[string]any) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !m.Ready() {
		return "", errors.New("采集脚本未就绪")
	}
	// 排队上限:解析链路全局串行,过多排队直接拒绝,防止并发打满连接
	select {
	case m.waitSem <- struct{}{}:
		defer func() { <-m.waitSem }()
	case <-ctx.Done():
		return "", ctx.Err()
	default:
		return "", errors.New("采集脚本排队已满")
	}
	select {
	case m.callSem <- struct{}{}:
		defer func() { <-m.callSem }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	if !m.Ready() {
		return "", errors.New("采集脚本未就绪")
	}
	callCtx, cancelCall := context.WithCancel(ctx)
	stopCancel := context.AfterFunc(m.ctx, cancelCall)
	defer func() { cancelCall(); stopCancel() }()

	resCh := make(chan urlResult, 1)
	sendRes := func(r urlResult) {
		select {
		case resCh <- r:
		default: // 超时后的迟到结果丢弃
		}
	}

	ok := m.runInLoop(func(vm *goja.Runtime) {
		if err := callCtx.Err(); err != nil {
			sendRes(urlResult{err: err.Error()})
			return
		}
		m.withRequestScope(callCtx, func() {
			handler, has := m.requestHandler()
			if !has {
				sendRes(urlResult{err: "脚本未注册 request 处理器"})
				return
			}
			payload := vm.NewObject()
			payload.Set("source", source)
			payload.Set("action", "musicUrl")
			info := vm.NewObject()
			if quality == "" {
				info.Set("type", goja.Null())
			} else {
				info.Set("type", quality)
			}
			info.Set("musicInfo", vm.ToValue(musicInfo))
			payload.Set("info", info)

			// 直接在 Go 侧调用脚本注册的 handler,拿到 Promise 再交给 JS 侧 settle
			result, err := handler(goja.Undefined(), payload)
			if err != nil {
				sendRes(urlResult{err: err.Error()})
				return
			}
			// Pass each call's result and callback through a local closure. Nothing is
			// rooted in globalThis, including promises that never settle.
			settle := vm.ToValue(func(call goja.FunctionCall) goja.Value {
				r := urlResult{}
				if !isNothing(call.Argument(0)) {
					r.err = call.Argument(0).String()
				}
				v := call.Argument(1)
				if !isNothing(v) {
					switch x := v.Export().(type) {
					case string:
						r.url = x
					case map[string]any:
						if u, ok := x["url"].(string); ok {
							r.url = u
						}
					}
				}
				sendRes(r)
				return goja.Undefined()
			})

			bridge, bridgeErr := vm.RunString(`(function(result, settle){
			try {
				Promise.resolve(result).then(
					function(v){ settle(null, v) },
					function(e){ settle(String(e && e.message || e), null) }
				);
			} catch (e) {
				settle(String(e && e.message || e), null);
			}
		})`)
			if bridgeErr != nil {
				sendRes(urlResult{err: bridgeErr.Error()})
			} else if fn, ok := goja.AssertFunction(bridge); ok {
				if _, err := fn(goja.Undefined(), result, settle); err != nil {
					sendRes(urlResult{err: err.Error()})
				}
			}
		})
	})
	if !ok {
		return "", errors.New("脚本事件循环已终止")
	}

	// 单次调用的硬上限:ctx 决定"还值不值得等",timer 决定"这个脚本最长跑多久"
	timer := time.NewTimer(m.Timeout)
	defer timer.Stop()

	select {
	case r := <-resCh:
		if r.err != "" {
			return "", fmt.Errorf("采集脚本: %s", r.err)
		}
		if r.url == "" || !strings.HasPrefix(r.url, "http") {
			return "", errors.New("脚本未返回有效的播放地址")
		}
		return r.url, nil
	case <-m.ctx.Done():
		return "", errors.New("采集脚本已停止")
	case <-timer.C:
		cancelCall()
		// Interrupt 是 goroutine 安全的,必须直调:包进 RunOnLoop 会排在卡死的
		// JS 任务之后,脚本同步死循环时永远执行不到。
		// 不能紧跟 ClearInterrupt(两写背靠背,VM 轮询大概率错过,中断被吞);
		// goja 的 InterruptedError 会作为本次 JS 调用的 error 返回,事件循环自行恢复,
		// 残留标志由 runInLoop 在每个新任务开头统一清除。
		return m.abortCall("获取播放地址超时")
	case <-ctx.Done():
		cancelCall()
		// 调用方的整体预算耗尽(或客户端已断开):不再空等 m.Timeout,
		// 否则一次请求能拖到"脚本数 × Timeout",连接与 goroutine 都被长期占用。
		// 若结果恰好已就绪,优先采用,避免白白丢掉一次成功的解析。
		select {
		case r := <-resCh:
			if r.err != "" {
				return "", fmt.Errorf("采集脚本: %s", r.err)
			}
			if r.url == "" || !strings.HasPrefix(r.url, "http") {
				return "", errors.New("脚本未返回有效的播放地址")
			}
			return r.url, nil
		default:
		}
		return m.abortCall("请求已超时或已取消,已停止解析")
	}
}

// abortCall 中断仍在 VM 中运行的 JS,并投递屏障确认事件循环回到空闲。
// 关键:callSem 会在调用方返回时释放,若不先确认 VM 空闲,
// 下一次 MusicURL 就可能与这次未结束的 JS 任务并发进入同一 VM。
func (m *Manager) abortCall(reason string) (string, error) {
	m.interrupt(reason)
	if !m.drainLoop(drainBarrierTimeout) {
		m.poisoned.Store(true)
		m.mu.Lock()
		if m.InitErr == nil {
			m.InitErr = errors.New("脚本超时后无响应,已停用,请在系统设置中重试加载")
		}
		m.mu.Unlock()
		log.Printf("[采集接口] %s 超时后事件循环无响应,已熔断该音源(请在系统设置中重试加载)", m.Name)
		return "", errors.New("采集脚本无响应,该音源已暂时停用")
	}
	return "", errors.New(reason)
}

// drainBarrierTimeout 等待事件循环处理完残留任务的上限
const drainBarrierTimeout = 3 * time.Second

// requestHandler 取脚本注册的 request 处理器(加锁,避免与重载时替换 map 竞态)
func (m *Manager) requestHandler() (goja.Callable, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	h, ok := m.handlers["request"]
	return h, ok
}

// jsHTTPClient 脚本 HTTP 请求复用的客户端(连接池复用)。
// 必须走带拨号复检的安全传输层:脚本内容来自第三方,不能让它访问内网/回环/云元数据地址。
var jsHTTPClient = music.NewSafeClient(60 * time.Second)
var scriptHTTPClient = music.NewSafeClient(120 * time.Second)
var scriptHTTPSem = make(chan struct{}, 32)

// callCB 在事件循环内调用 JS 回调 callback(err, resp, body)
func callCB(_ *goja.Runtime, cb goja.Callable, args ...goja.Value) {
	defer func() { _ = recover() }() // 回调抛错只影响脚本自身,不能拖垮宿主
	cb(goja.Undefined(), args...)
}

// jsRequest 实现 lx.request(url, options, callback) -> cancel 函数
func (m *Manager) jsRequest(vm *goja.Runtime) func(call goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		rawURL := call.Argument(0).String()
		opts := map[string]any{}
		if o := call.Argument(1); !isNothing(o) {
			if ex, ok := o.Export().(map[string]any); ok {
				opts = ex
			}
		}
		cb, cbOK := goja.AssertFunction(call.Argument(2))
		if p, err := url.Parse(rawURL); err != nil || p.Host == "" || p.User != nil || (p.Scheme != "http" && p.Scheme != "https") {
			panic(vm.NewTypeError("请求地址非法(仅支持 http/https)"))
		}
		method, body, contentType, timeout := buildRequest(opts)
		req, err := http.NewRequest(strings.ToUpper(method), rawURL, strings.NewReader(body))
		if err != nil {
			panic(vm.NewGoError(err))
		}
		req.Header.Set("User-Agent", hostUA)
		if hv, ok := opts["headers"].(map[string]any); ok {
			for k, v := range hv {
				req.Header.Set(k, fmt.Sprint(v))
			}
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		parent := m.requestCtx // VM-thread-only scope, inherited by callbacks/timers.
		if parent.Err() != nil {
			panic(vm.NewGoError(parent.Err()))
		}
		select {
		case m.httpSem <- struct{}{}:
		default:
			panic(vm.NewGoError(errors.New("脚本并发请求过多")))
		}
		select {
		case scriptHTTPSem <- struct{}{}:
		default:
			<-m.httpSem
			panic(vm.NewGoError(errors.New("脚本 HTTP 服务繁忙")))
		}
		var once sync.Once
		release := func() { once.Do(func() { <-m.httpSem; <-scriptHTTPSem }) }
		stopRelease := context.AfterFunc(parent, release)
		finish := func() { stopRelease(); release() }
		// Slots include responses waiting for their callback; otherwise a busy VM
		// could accumulate unbounded buffered bodies behind a fast HTTP server.
		deliver := func(fn func(*goja.Runtime)) {
			if cbOK && parent.Err() == nil && m.runInLoop(func(vm *goja.Runtime) {
				// Release before invoking JS so a callback can start its next request
				// even when every slot was occupied by a completed response.
				finish()
				if parent.Err() == nil {
					m.withRequestScope(parent, func() { fn(vm) })
				}
			}) {
				return
			}
			finish()
		}
		ctx, cancel := context.WithTimeout(parent, timeout)
		req = req.WithContext(ctx)
		client := jsHTTPClient
		go func() {
			defer cancel()
			resp, err := client.Do(req)
			if err != nil {
				deliver(func(vm *goja.Runtime) { callCB(vm, cb, vm.ToValue(err.Error()), goja.Null(), goja.Null()) })
				return
			}
			defer resp.Body.Close()
			const maxBody = 20 << 20
			b, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
			if readErr == nil && len(b) > maxBody {
				readErr = errors.New("脚本响应超过 20 MiB")
			}
			if readErr != nil {
				deliver(func(vm *goja.Runtime) { callCB(vm, cb, vm.ToValue(readErr.Error()), goja.Null(), goja.Null()) })
				return
			}
			headers := map[string]string{}
			for k, v := range resp.Header {
				if len(v) > 0 {
					headers[k] = v[0]
				}
			}
			statusCode, bodyStr := resp.StatusCode, string(b)
			deliver(func(vm *goja.Runtime) {
				respObj := vm.NewObject()
				respObj.Set("statusCode", statusCode)
				respObj.Set("headers", headers)
				respObj.Set("body", bodyStr)
				callCB(vm, cb, goja.Null(), respObj, vm.ToValue(bodyStr))
			})
		}()
		return vm.ToValue(func(goja.FunctionCall) goja.Value { cancel(); return goja.Undefined() })
	}
}

// withRequestScope is used only on the VM thread, including asynchronous
// callbacks. Initial/background work retains the manager's lifetime.
func (m *Manager) withRequestScope(ctx context.Context, fn func()) {
	previous := m.requestCtx
	m.requestCtx = ctx
	defer func() { m.requestCtx = previous }()
	fn()
}

// buildRequest 从脚本传来的 options 中提取 method/body/contentType/timeout
func buildRequest(opts map[string]any) (method, body, contentType string, timeout time.Duration) {
	method = "GET"
	if m, ok := opts["method"].(string); ok && m != "" {
		method = m
	}
	if t, ok := toFloat(opts["timeout"]); ok && t > 0 {
		timeout = time.Duration(t) * time.Millisecond
	}
	if timeout <= 0 || timeout > 60*time.Second {
		timeout = 20 * time.Second
	}
	if s, ok := opts["body"].(string); ok {
		body = s
		return
	}
	if form, ok := opts["form"].(map[string]any); ok {
		vals := url.Values{}
		for k, v := range form {
			vals.Set(k, fmt.Sprint(v))
		}
		body = vals.Encode()
		contentType = "application/x-www-form-urlencoded"
		return
	}
	if form, ok := opts["formData"].(map[string]any); ok {
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		for k, v := range form {
			switch x := v.(type) {
			case map[string]any: // { value, filename, contentType }
				var val string
				if s, ok := x["value"].(string); ok {
					val = s
				}
				filename := k
				if s, ok := x["filename"].(string); ok && s != "" {
					filename = s
				}
				part, err := w.CreateFormFile(k, filename)
				if err == nil {
					part.Write([]byte(val))
				}
			default:
				w.WriteField(k, fmt.Sprint(v))
			}
		}
		w.Close()
		body = buf.String()
		contentType = w.FormDataContentType()
	}
	return
}
