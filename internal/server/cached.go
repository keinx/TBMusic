package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"tbmusic/internal/music"
	"tbmusic/internal/store"
)

type cacheFlight struct {
	done chan struct{}
	data []byte
	err  error
}

const maxMetadataLoads = 32

var errMetadataBusy = errors.New("元数据请求繁忙，请稍后重试")

func requestKey(kind string, parts ...any) string {
	b, _ := json.Marshal(parts)
	sum := sha256.Sum256(b)
	return kind + "|" + hex.EncodeToString(sum[:])
}
func (s *Server) cached(ctx context.Context, key string, ttl time.Duration, load func() ([]byte, error)) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if b, ok := s.Cache.Get(key); ok {
		return b, nil
	}
	s.flightMu.Lock()
	if f := s.flights[key]; f != nil {
		s.flightMu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-f.done:
			return f.data, f.err
		}
	}
	if len(s.flights) >= maxMetadataLoads {
		s.flightMu.Unlock()
		return nil, errMetadataBusy
	}
	f := &cacheFlight{done: make(chan struct{})}
	s.flights[key] = f
	s.flightMu.Unlock()
	defer func() { s.flightMu.Lock(); delete(s.flights, key); close(f.done); s.flightMu.Unlock() }()
	if b, ok := s.Cache.Get(key); ok {
		f.data = b
		return b, nil
	}
	generation := s.Cache.Generation()
	if err := ctx.Err(); err != nil {
		f.err = err
		return nil, err
	}
	f.data, f.err = load()
	if err := ctx.Err(); err != nil {
		f.data, f.err = nil, err
	}
	if f.err == nil && len(f.data) > 0 {
		if err := s.Cache.SetIfGeneration(key, f.data, ttl, "", generation); err != nil {
			log.Printf("[cache] 写入失败: %v", err)
		}
	}
	return f.data, f.err
}
func (s *Server) cachedJSON(w http.ResponseWriter, r *http.Request, key string, ttl time.Duration, load func() (any, error)) {
	b, err := s.cached(r.Context(), key, ttl, func() ([]byte, error) {
		v, err := load()
		if err != nil {
			return nil, err
		}
		return json.Marshal(v)
	})
	if err != nil {
		if errors.Is(err, errMetadataBusy) || errors.Is(err, music.ErrBusy) {
			writeErr(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		if errors.Is(err, music.ErrUnsupported) {
			writeErr(w, 400, err.Error())
			return
		}
		if r.Context().Err() != nil {
			return
		}
		log.Printf("[music] 上游请求失败: %v", err)
		writeErr(w, 502, "上游接口暂时不可用,请稍后重试")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(200)
	_, _ = w.Write(b)
}
func pageResult[T any](p *music.Page[T], err error) (any, error) {
	if err != nil {
		return nil, err
	}
	if p == nil {
		p = &music.Page[T]{}
	}
	if p.List == nil {
		p.List = []T{}
	}
	if p.Page >= 100 {
		p.HasMore = false
	}
	return p, nil
}
func (s *Server) handleCacheStats(w http.ResponseWriter, r *http.Request, u *store.User) {
	writeJSON(w, 200, s.Cache.Stats())
}

var policyHost = regexp.MustCompile(`^(\*\.)?[a-zA-Z0-9.-]+$`)

// Permit direct media only for configured audio domains; scripts/connect remain same-origin.
func (s *Server) mediaPolicy() string {
	out := []string{"'self'", "blob:"}
	for _, host := range s.Cfg.GetStreamHosts() {
		if host == "*" {
			return "'self' blob: https: http:"
		}
		if !policyHost.MatchString(host) {
			continue
		}
		for _, scheme := range []string{"https://", "http://"} {
			out = append(out, scheme+host+":*")
			if strings.HasPrefix(host, "*.") {
				out = append(out, scheme+host[2:]+":*")
			}
		}
	}
	return strings.Join(out, " ")
}
