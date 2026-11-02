package lxsource

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"time"
)

// ErrNoSource 所有采集脚本都拿不到可用地址
var ErrNoSource = errors.New("当前歌曲没有可用资源")

// ErrNotReady 没有任何采集脚本就绪
var ErrNotReady = errors.New("采集脚本未就绪")

// MaxManagers 采集脚本数量上限(每个脚本一个 JS 运行时,过多会吃内存)。
// 与后台设置页使用的是同一个上限,避免重试加载时绕过校验无限追加。
const MaxManagers = 24

// Pool 维护一组采集脚本,第 1 个为主音源;主音源取链失败时,
// 在其余子音源中随机顺序依次尝试(每个脚本只试一次,不重复)。
type Pool struct {
	mu   sync.RWMutex
	mgrs []*Manager
	// epoch 每次 Set(全量重建)递增。Retry 在耗时加载前后各比对一次,
	// 避免把实例插进"已经被替换掉"的旧池子:那样新实例会滞留在池外的
	// 事件循环里(goroutine/VM 泄漏),或反过来被 Set 停掉后仍留在池中被调用。
	epoch uint64
}

func NewPool() *Pool { return &Pool{} }

// Set 替换整个脚本池;旧实例在替换后统一停止
func (p *Pool) Set(list []*Manager) {
	p.set(list, false)
}

// SetPreservingReady keeps the current working instance if the same URL fails
// to initialize. The merge occurs under the pool lock so Retry cannot race it.
func (p *Pool) SetPreservingReady(list []*Manager) { p.set(list, true) }

func (p *Pool) set(list []*Manager, preserve bool) {
	p.mu.Lock()
	old := p.mgrs
	var discarded []*Manager
	if preserve {
		for i, next := range list {
			if next == nil || next.Ready() {
				continue
			}
			for _, previous := range old {
				if previous != nil && previous.URL == next.URL && previous.Ready() {
					list[i] = previous
					discarded = append(discarded, next)
					break
				}
			}
		}
	}
	retained := make(map[*Manager]bool, len(list))
	for _, m := range list {
		retained[m] = true
	}
	p.mgrs = list
	p.epoch++
	p.mu.Unlock()
	for _, m := range append(old, discarded...) {
		if m != nil && !retained[m] {
			m.Stop()
		}
	}
}

// All 返回全部脚本(含未就绪的),顺序即配置顺序
func (p *Pool) All() []*Manager {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]*Manager, len(p.mgrs))
	copy(out, p.mgrs)
	return out
}

// Primary 主音源:配置里的第一个脚本
func (p *Pool) Primary() *Manager {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if len(p.mgrs) == 0 {
		return nil
	}
	return p.mgrs[0]
}

// PrimaryReady 主音源(未就绪时返回 nil,由调用方决定是否退到子音源)
func (p *Pool) PrimaryReady() *Manager {
	if m := p.Primary(); m != nil && m.Ready() {
		return m
	}
	return nil
}

// SubsShuffled 返回除主音源外的就绪脚本,顺序随机
func (p *Pool) SubsShuffled() []*Manager {
	all := p.All()
	if len(all) <= 1 {
		return nil
	}
	out := make([]*Manager, 0, len(all)-1)
	for _, m := range all[1:] {
		if m.Ready() {
			out = append(out, m)
		}
	}
	rand.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

// ReadyCount 就绪脚本数量
func (p *Pool) ReadyCount() int {
	n := 0
	for _, m := range p.All() {
		if m.Ready() {
			n++
		}
	}
	return n
}

// Retry 重新加载指定 URL 的脚本,成功后原位替换旧实例(保持优先级顺序)。
// 旧实例即使是"加载失败"的空壳也会被替换;替换后统一停止旧实例。
// 若加载期间发生过全量重载(Set),本次结果作废:此时池子已经是新的一批实例,
// 强行插入会丢失更新或留下无人引用的运行时。
func (p *Pool) Retry(name, rawURL string, timeout time.Duration) error {
	p.mu.RLock()
	gen := p.epoch
	p.mu.RUnlock()

	m := NewManager(name, rawURL, timeout)
	if err := m.Reload(); err != nil { // 强制刷新,绕过本地缓存
		m.Stop()
		return err
	}

	p.mu.Lock()
	if p.epoch != gen {
		// 加载期间全量重建过:丢弃本次结果(池里的同名脚本以新重载为准)
		p.mu.Unlock()
		m.Stop()
		return errors.New("采集脚本池已被重新加载,请重试")
	}
	var old *Manager
	replaced := false
	for i, x := range p.mgrs {
		if x != nil && x.URL == rawURL {
			old = x
			p.mgrs[i] = m
			replaced = true
			break
		}
	}
	if !replaced {
		// 池里没有该 URL(理论不可达:全量重载会保留失败实例):
		// 追加到末尾,至少让脚本可用;但不得超过脚本数量上限。
		if len(p.mgrs) >= MaxManagers {
			p.mu.Unlock()
			m.Stop()
			return fmt.Errorf("采集脚本数量已达上限(%d 个)", MaxManagers)
		}
		p.mgrs = append(p.mgrs, m)
	}
	p.mu.Unlock()

	if old != nil {
		old.Stop()
	}
	return nil
}

// Close 停止全部脚本
func (p *Pool) Close() {
	p.Set(nil)
}

// FirstInitErr 返回第一个就绪失败脚本的错误(供后台展示原因)
func (p *Pool) FirstInitErr() error {
	for _, m := range p.All() {
		if err := m.GetInitErr(); err != nil {
			return err
		}
	}
	return nil
}

// ctxDone 供调用方在脚本轮询中快速退出
func ctxDone(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}
