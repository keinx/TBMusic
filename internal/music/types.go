// Package music 定义内置音源的通用接口与数据结构。
// musicInfo 字段与 lx-music-desktop 兼容,以便自定义音源脚本能解析播放地址。
package music

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Song 歌曲(lx 兼容 musicInfo)
type Song struct {
	Source      string   `json:"source"`
	Songmid     string   `json:"songmid"`
	SongID      string   `json:"songId"`
	AlbumID     string   `json:"albumId"`
	AlbumMid    string   `json:"albumMid,omitempty"`
	AlbumName   string   `json:"albumName"`
	Hash        string   `json:"hash,omitempty"`        // kg: 文件 hash
	CopyrightID string   `json:"copyrightId,omitempty"` // mg: copyrightId
	Name        string   `json:"name"`
	Singer      string   `json:"singer"`
	Interval    string   `json:"interval"` // mm:ss
	Pic         string   `json:"pic,omitempty"`
	LrcURL      string   `json:"lrcUrl,omitempty"` // mg 歌词直链
	Qualities   []string `json:"qualities,omitempty"`
}

// Key 用于收藏/拉黑/历史的唯一标识
func (s *Song) Key() string {
	id := s.Songmid
	if id == "" && s.Hash != "" {
		id = s.Hash
	}
	if id == "" && s.CopyrightID != "" {
		id = s.CopyrightID
	}
	return s.Source + ":" + id
}

type Album struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Singer  string `json:"singer"`
	Pic     string `json:"pic"`
	Publish string `json:"publish,omitempty"`
}

type Songlist struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Creator string `json:"creator"`
	Pic     string `json:"pic"`
	Count   int    `json:"count,omitempty"`
}

type Artist struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Pic  string `json:"pic"`
}

// Toplist 热门榜单
type Toplist struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Pic  string `json:"pic"`
	Freq string `json:"freq,omitempty"`
}

type Page[T any] struct {
	List    []T  `json:"list"`
	Total   int  `json:"total"`
	Page    int  `json:"page"`
	Limit   int  `json:"limit"`
	HasMore bool `json:"hasMore"`
}

type Lyrics struct {
	Lyric  string `json:"lyric"`
	TLyric string `json:"tlyric"`
}

var ErrUnsupported = errors.New("该音源暂不支持此功能")

// Source 内置音源接口;未支持的方法返回 ErrUnsupported
type Source interface {
	ID() string
	Name() string
	SearchTypes() []string
	SearchMusic(ctx context.Context, keyword string, page, limit int) (*Page[Song], error)
	SearchAlbum(ctx context.Context, keyword string, page, limit int) (*Page[Album], error)
	SearchSonglist(ctx context.Context, keyword string, page, limit int) (*Page[Songlist], error)
	SearchArtist(ctx context.Context, keyword string, page, limit int) (*Page[Artist], error)
	SearchLyric(ctx context.Context, keyword string, page, limit int) (*Page[Song], error)
	AlbumSongs(ctx context.Context, albumID string, page, limit int) (*Page[Song], error)
	SonglistSongs(ctx context.Context, listID string, page, limit int) (*Page[Song], error)
	ArtistSongs(ctx context.Context, artistID string, page, limit int) (*Page[Song], error)
	GetLyric(ctx context.Context, song *Song) (*Lyrics, error)
	GetPic(ctx context.Context, song *Song) (string, error)
}

// Base 提供默认的"不支持"实现,各音源按需覆盖
type Base struct{}

func (Base) SearchMusic(context.Context, string, int, int) (*Page[Song], error) {
	return nil, ErrUnsupported
}
func (Base) SearchAlbum(context.Context, string, int, int) (*Page[Album], error) {
	return nil, ErrUnsupported
}
func (Base) SearchSonglist(context.Context, string, int, int) (*Page[Songlist], error) {
	return nil, ErrUnsupported
}
func (Base) SearchArtist(context.Context, string, int, int) (*Page[Artist], error) {
	return nil, ErrUnsupported
}
func (Base) SearchLyric(context.Context, string, int, int) (*Page[Song], error) {
	return nil, ErrUnsupported
}
func (Base) AlbumSongs(context.Context, string, int, int) (*Page[Song], error) {
	return nil, ErrUnsupported
}
func (Base) SonglistSongs(context.Context, string, int, int) (*Page[Song], error) {
	return nil, ErrUnsupported
}
func (Base) ArtistSongs(context.Context, string, int, int) (*Page[Song], error) {
	return nil, ErrUnsupported
}
func (Base) GetLyric(context.Context, *Song) (*Lyrics, error) { return nil, ErrUnsupported }
func (Base) GetPic(context.Context, *Song) (string, error)    { return "", ErrUnsupported }
func (Base) SearchTypes() []string                            { return nil }

const DefaultUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

// Client 请求各平台公开接口的共享 HTTP 客户端
var Client = &http.Client{Timeout: 12 * time.Second}

// StreamHeaders 各音源音频直链所需的防盗链请求头
func StreamHeaders(source string) map[string]string {
	switch source {
	case "kw":
		return map[string]string{"Referer": "http://www.kuwo.cn/"}
	case "tx":
		return map[string]string{"Referer": "https://y.qq.com/"}
	case "wy":
		return map[string]string{"Referer": "https://music.163.com/"}
	case "kg":
		return map[string]string{"Referer": "https://www.kugou.com/"}
	case "mg":
		return map[string]string{"Referer": "https://music.migu.cn/"}
	}
	return nil
}

// safeDialer 在每次实际拨号时复检解析出的 IP,封堵 DNS 重绑定绕过(校验与连接间改解析)
var safeDialer = &net.Dialer{
	Timeout:   10 * time.Second,
	KeepAlive: 30 * time.Second,
}

func safeDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, ia := range ips {
		ip := ia.IP
		if !IsPublicIP(ip) {
			lastErr = fmt.Errorf("内网地址不允许")
			continue
		}
		conn, derr := safeDialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if derr == nil {
			return conn, nil
		}
		lastErr = derr
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("无可用安全地址")
	}
	return nil, lastErr
}

// SafeHTTPClient 强制所有连接走拨号复检的客户端(供代理/歌词等外发场景)。
// 不设整体 Timeout:音频流是长连接,由 ResponseHeaderTimeout + 调用方的读空闲超时兜底。
var SafeHTTPClient = &http.Client{
	Transport: &http.Transport{
		DialContext:           safeDialContext,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 5 {
			return fmt.Errorf("重定向过多")
		}
		// 每一跳都复查目标,防 302 跳转内网绕过首跳校验
		if err := SafeOutboundURLContext(req.Context(), req.URL.String()); err != nil {
			return err
		}
		return nil
	},
}

// NewSafeClient 返回带拨号复检(防 SSRF/DNS 重绑定)的客户端,可设置整体超时。
// 复用 SafeHTTPClient 的连接池与逐跳重定向校验,供脚本宿主等请求方可控 URL 的场景使用。
func NewSafeClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Transport:     SafeHTTPClient.Transport,
		Timeout:       timeout,
		CheckRedirect: SafeHTTPClient.CheckRedirect,
	}
}

// dnsVerdictCache 缓存 SafeOutboundURL 的预检结论。
// 校验"通过"不是安全授权的依据(真正连接时 safeDialContext 会再校验一次实际 IP),
// 缓存只是避免同一主机的两次解析,以及给恶意请求快速拒绝。
type dnsVerdict struct {
	err  error
	exp  time.Time
	elem *list.Element
}

var dnsGuard struct {
	mu    sync.Mutex
	m     map[string]dnsVerdict
	order list.List
}

const dnsGuardTTL = 3 * time.Minute
const dnsGuardCapacity = 4096

// lookupHostVerdict 带 TTL 缓存的主机安全校验
func lookupHostVerdict(host string) error {
	return lookupHostVerdictContext(context.Background(), host)
}

func lookupHostVerdictContext(ctx context.Context, host string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	now := time.Now()
	dnsGuard.mu.Lock()
	if dnsGuard.m == nil {
		dnsGuard.m = make(map[string]dnsVerdict)
		dnsGuard.order.Init()
	}
	if v, ok := dnsGuard.m[host]; ok && now.Before(v.exp) {
		dnsGuard.order.MoveToFront(v.elem)
		dnsGuard.mu.Unlock()
		return v.err
	}
	dnsGuard.mu.Unlock()

	err := func() error {
		ips, lerr := net.DefaultResolver.LookupIPAddr(ctx, host)
		if lerr != nil {
			return fmt.Errorf("主机解析失败")
		}
		for _, ip := range ips {
			if !IsPublicIP(ip.IP) {
				return fmt.Errorf("内网地址不允许")
			}
		}
		return nil
	}()
	if ctx.Err() != nil {
		return ctx.Err()
	}

	dnsGuard.mu.Lock()
	if old, ok := dnsGuard.m[host]; ok {
		dnsGuard.order.Remove(old.elem)
		delete(dnsGuard.m, host)
	}
	for len(dnsGuard.m) >= dnsGuardCapacity {
		last := dnsGuard.order.Back()
		delete(dnsGuard.m, last.Value.(string))
		dnsGuard.order.Remove(last)
	}
	dnsGuard.m[host] = dnsVerdict{err: err, exp: time.Now().Add(dnsGuardTTL), elem: dnsGuard.order.PushFront(host)}
	dnsGuard.mu.Unlock()
	return err
}

// IsPublicIP 是否为可外发的公网地址
func IsPublicIP(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() {
		return false
	}
	for _, prefix := range nonPublicPrefixes {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

// Special-purpose networks are not ordinary public destinations. Include
// translation/transition prefixes so an embedded private IPv4 address cannot
// bypass the same policy through IPv6.
var nonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"), netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"), netip.MustParsePrefix("::/96"),
	netip.MustParsePrefix("64:ff9b::/96"), netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"), netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("3fff::/20"),
}

// SafeOutboundURL 校验外发 URL:仅 http(s),且目标不能是内网/回环/链路本地地址(防 SSRF)
func SafeOutboundURL(rawURL string) error {
	return SafeOutboundURLContext(context.Background(), rawURL)
}

func SafeOutboundURLContext(ctx context.Context, rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return fmt.Errorf("url 非法")
	}
	host := parsed.Hostname()
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return fmt.Errorf("目标主机不允许")
	}
	return lookupHostVerdictContext(ctx, host)
}

// PageSlice 对全量列表做通用分页切片(歌单/歌手详情等一次性拉全量的场景)
func PageSlice[T any](list []T, page, limit int) []T {
	if limit <= 0 {
		limit = 30
	}
	if page <= 0 {
		page = 1
	}
	start := (page - 1) * limit
	if start > len(list) {
		start = len(list)
	}
	end := start + limit
	if end > len(list) {
		end = len(list)
	}
	return list[start:end]
}

func FormatInterval(sec int) string {
	if sec <= 0 {
		return "--:--"
	}
	return fmt.Sprintf("%d:%02d", sec/60, sec%60)
}
