package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Script 一个 LX 自定义音源脚本(采集接口)
type Script struct {
	Name string `yaml:"name"`
	URL  string `yaml:"url"`
}

const DefaultCacheSizeMB int64 = 3072
const MaxCacheSizeMB int64 = 1024 * 1024

type Config struct {
	mu sync.RWMutex // 保护 Auth/Source 的并发读写(admin 写 vs 请求读)

	Server struct {
		Addr string `yaml:"addr"`
		// TrustedProxies 可信反向代理网段(CIDR)。仅当 TCP 对端落在这些网段内时,
		// 才会解析 X-Forwarded-For;否则一律使用对端地址,防止请求头伪造绕过限流。
		TrustedProxies []string `yaml:"trustedProxies"`
	} `yaml:"server"`

	Source struct {
		Timeout time.Duration `yaml:"timeout"`
		Scripts []Script      `yaml:"scripts"`
	} `yaml:"source"`

	// Stream 音频代理白名单:仅允许代理这些域名(支持 *.example.com 通配子域)
	Stream struct {
		AllowHosts []string `yaml:"allowHosts"`
	} `yaml:"stream"`

	Database struct {
		Path string `yaml:"path"`
	} `yaml:"database"`

	Session struct {
		Days int `yaml:"days"`
	} `yaml:"session"`

	Auth struct {
		AllowRegister bool `yaml:"allowRegister"`
		// 邀请码注册:开启后前台注册需填写正确的邀请码
		InviteRequired bool   `yaml:"inviteRequired"`
		InviteCode     string `yaml:"inviteCode"`
	} `yaml:"auth"`

	Cache struct {
		Dir       string        `yaml:"dir,omitempty"`
		SizeMB    int64         `yaml:"sizeMB"`
		UrlTTL    time.Duration `yaml:"urlTTL"`
		LyricTTL  time.Duration `yaml:"lyricTTL"`
		SearchTTL time.Duration `yaml:"searchTTL"`
		DetailTTL time.Duration `yaml:"detailTTL"`
		CoverTTL  time.Duration `yaml:"coverTTL"`
	} `yaml:"cache"`
}

// DefaultStreamHosts 默认音频 CDN 白名单(覆盖内置五个音源与常见中转域名)。
// 管理后台可逐条增删;单条 "*" 表示放开全部域名(不建议)。
func DefaultStreamHosts() []string {
	return []string{
		"*.kuwo.cn",   // 酷我
		"*.kugou.com", // 酷狗
		"*.kgimg.com", // 酷狗图片/音频 CDN
		"*.qq.com",    // QQ 音乐(含 y.qq.com / isure.stream.qqmusic.qq.com)
		"*.gtimg.cn",  // QQ 音乐封面
		"*.126.net",   // 网易云音频/封面 CDN
		"*.127.net",   // 网易云音频/封面 CDN
		"*.migu.cn",   // 咪咕
		"*.migucloud.com",
	}
}

func Default() *Config {
	c := &Config{}
	c.Server.Addr = "0.0.0.0:8080"
	c.Source.Timeout = 15 * time.Second
	c.Database.Path = "./data/tbmusic.db"
	c.Session.Days = 30
	c.Auth.AllowRegister = true
	c.Cache.SizeMB = DefaultCacheSizeMB
	c.Cache.UrlTTL = 20 * time.Minute
	c.Cache.LyricTTL = 24 * time.Hour
	c.Cache.SearchTTL = 10 * time.Minute
	c.Cache.DetailTTL = 30 * time.Minute
	c.Cache.CoverTTL = 7 * 24 * time.Hour
	c.Stream.AllowHosts = DefaultStreamHosts()
	return c
}

func Load(path string) (*Config, error) {
	c := Default()
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return c, nil
		}
		return nil, err
	}
	if err := yaml.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("解析配置 %s: %w", path, err)
	}
	if c.Server.Addr == "" {
		c.Server.Addr = "0.0.0.0:8080"
	}
	if c.Database.Path == "" {
		c.Database.Path = "./data/tbmusic.db"
	}
	if c.Session.Days <= 0 {
		c.Session.Days = 30
	}
	if c.Source.Timeout <= 0 {
		c.Source.Timeout = 15 * time.Second
	}
	if c.Cache.SizeMB < 1 || c.Cache.SizeMB > MaxCacheSizeMB {
		return nil, fmt.Errorf("cache.sizeMB 必须是 1-%d 之间的整数", MaxCacheSizeMB)
	}
	if c.Cache.UrlTTL <= 0 {
		c.Cache.UrlTTL = 20 * time.Minute
	}
	if c.Cache.LyricTTL <= 0 {
		c.Cache.LyricTTL = 24 * time.Hour
	}
	if c.Cache.SearchTTL <= 0 {
		c.Cache.SearchTTL = 10 * time.Minute
	}
	if c.Cache.DetailTTL <= 0 {
		c.Cache.DetailTTL = 30 * time.Minute
	}
	if c.Cache.CoverTTL <= 0 {
		c.Cache.CoverTTL = 7 * 24 * time.Hour
	}
	// nil 表示配置文件里没有该键 → 补默认值;
	// 显式写成 [] 则保留为空(表示禁止一切代理),便于管理员彻底关闭音频代理。
	if c.Stream.AllowHosts == nil {
		c.Stream.AllowHosts = DefaultStreamHosts()
	}
	return c, nil
}

// Save 原子落盘:先写同目录临时文件并 fsync,再 rename 覆盖目标。
// 直接 os.WriteFile 会先把原文件截断为 0,进程崩溃或并发写会让 config.yaml
// 变成半截内容(白名单/邀请码/脚本列表全部丢失)。
func (c *Config) Save(path string) error {
	c.mu.Lock()
	b, err := yaml.Marshal(c)
	c.mu.Unlock()
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// 任一步失败都要清掉临时文件,避免残留
	defer func() {
		if tmpName != "" {
			tmp.Close()
			os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(b); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	tmpName = "" // 已成功改名,defer 不再删除
	return nil
}

// ActiveScript 返回第一个启用的采集脚本
func (c *Config) ActiveScript() (Script, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, s := range c.Source.Scripts {
		if s.URL != "" {
			return s, true
		}
	}
	return Script{}, false
}

// ActiveScripts 返回全部启用的采集脚本快照(顺序即优先级)
func (c *Config) ActiveScripts() []Script {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Script, 0, len(c.Source.Scripts))
	for _, s := range c.Source.Scripts {
		if s.URL != "" {
			out = append(out, s)
		}
	}
	return out
}

// SourceTimeout 采集脚本单次调用超时
func (c *Config) SourceTimeout() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.Source.Timeout <= 0 {
		return 15 * time.Second
	}
	return c.Source.Timeout
}

// GetAuthSettings 返回认证配置快照(并发安全)
func (c *Config) GetAuthSettings() (allowRegister, inviteRequired bool, inviteCode string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Auth.AllowRegister, c.Auth.InviteRequired, c.Auth.InviteCode
}

// SetAuthSettings 更新认证配置(并发安全)
func (c *Config) SetAuthSettings(allowRegister, inviteRequired bool, inviteCode string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Auth.AllowRegister = allowRegister
	c.Auth.InviteRequired = inviteRequired
	c.Auth.InviteCode = inviteCode
}

// SetScripts 更新采集脚本列表(并发安全)
func (c *Config) SetScripts(scripts []Script) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Source.Scripts = scripts
}

// GetScripts 返回采集脚本原始列表快照(不过滤空 URL,供配置回滚使用)
func (c *Config) GetScripts() []Script {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Script, len(c.Source.Scripts))
	copy(out, c.Source.Scripts)
	return out
}

// GetStreamHosts 返回音频代理白名单快照(并发安全)
func (c *Config) GetStreamHosts() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, len(c.Stream.AllowHosts))
	copy(out, c.Stream.AllowHosts)
	return out
}

// SetStreamHosts 覆盖音频代理白名单(并发安全)
func (c *Config) SetStreamHosts(hosts []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Stream.AllowHosts = append([]string{}, hosts...)
}

func (c *Config) GetCacheSizeMB() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Cache.SizeMB
}

func (c *Config) SetCacheSizeMB(size int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Cache.SizeMB = size
}

// GetTrustedProxies 返回可信代理网段快照
func (c *Config) GetTrustedProxies() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, len(c.Server.TrustedProxies))
	copy(out, c.Server.TrustedProxies)
	return out
}

// StreamHostAllowed 判断主机是否命中白名单。
// 规则:* 匹配全部;*.example.com 匹配 example.com 及任意层级子域;其余精确匹配。
func StreamHostAllowed(host string, patterns []string) bool {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if host == "" {
		return false
	}
	for _, p := range patterns {
		p = strings.ToLower(strings.TrimSpace(p))
		switch {
		case p == "":
			continue
		case p == "*":
			return true
		case strings.HasPrefix(p, "*."):
			suffix := p[1:] // ".example.com"
			if host == suffix[1:] || strings.HasSuffix(host, suffix) {
				return true
			}
		case p == host:
			return true
		}
	}
	return false
}
