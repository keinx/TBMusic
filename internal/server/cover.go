package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"tbmusic/internal/config"
	"tbmusic/internal/music"
	"tbmusic/internal/store"
)

type resolvedURL struct {
	URL     string `json:"url"`
	Quality string `json:"quality"`
	Script  string `json:"script"`
}

func (s *Server) allowedPlayURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.User == nil && config.StreamHostAllowed(u.Hostname(), s.Cfg.GetStreamHosts())
}

// Covers, unlike audio, are small and cached in full. No HTML/SVG or upstream
// cookies are forwarded; SafeHTTPClient validates DNS and every redirect.
func (s *Server) handleCover(w http.ResponseWriter, r *http.Request, u *store.User) {
	raw := r.URL.Query().Get("url")
	if len(raw) > 4096 {
		writeErr(w, 400, "封面地址过长")
		return
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		writeErr(w, 400, "封面地址无效")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	data, err := s.cached(ctx, requestKey("cover", raw), s.Cfg.Cache.CoverTTL, func() ([]byte, error) {
		select {
		case s.coverSem <- struct{}{}:
			defer func() { <-s.coverSem }()
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if err := music.SafeOutboundURLContext(ctx, raw); err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", music.DefaultUA)
		resp, err := music.SafeHTTPClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return nil, errors.New("封面源暂时不可用")
		}
		const maxCover = 8 << 20
		b, err := io.ReadAll(io.LimitReader(resp.Body, maxCover+1))
		if err != nil {
			return nil, err
		}
		if len(b) > maxCover {
			return nil, errors.New("封面过大")
		}
		ct := http.DetectContentType(b)
		if !strings.HasPrefix(ct, "image/") {
			return nil, errors.New("封面格式不支持")
		}
		return append([]byte(ct+"\n"), b...), nil
	})
	if err != nil {
		writeErr(w, 502, "封面加载失败")
		return
	}
	ct, body, ok := bytes.Cut(data, []byte{'\n'})
	if !ok || !strings.HasPrefix(string(ct), "image/") {
		s.Cache.Delete(requestKey("cover", raw))
		writeErr(w, 502, "封面缓存无效")
		return
	}
	w.Header().Set("Content-Type", string(ct))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.WriteHeader(200)
	_, _ = w.Write(body)
}
