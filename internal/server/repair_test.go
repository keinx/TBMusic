package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"tbmusic/internal/music"
	"tbmusic/internal/music/mg"
	"tbmusic/internal/store"
)

func TestMetadataAdmissionAndCancellation(t *testing.T) {
	s := testCachedServer(t)
	entered, release := make(chan struct{}, maxMetadataLoads), make(chan struct{})
	var wg sync.WaitGroup
	defer func() { close(release); wg.Wait() }()
	for i := 0; i < maxMetadataLoads; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.cached(context.Background(), fmt.Sprint(i), time.Minute, func() ([]byte, error) { entered <- struct{}{}; <-release; return []byte(`{}`), nil })
			if err != nil {
				t.Error(err)
			}
		}(i)
	}
	for i := 0; i < maxMetadataLoads; i++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("load did not start")
		}
	}
	if _, err := s.cached(context.Background(), "excess", time.Minute, func() ([]byte, error) { t.Error("excess loader ran"); return nil, nil }); !errors.Is(err, errMetadataBusy) {
		t.Fatalf("admission: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.cached(ctx, "canceled", time.Minute, func() ([]byte, error) { t.Error("canceled loader ran"); return nil, nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestExpectedAccountPreventsCrossTabWrite(t *testing.T) {
	s := testCachedServer(t)
	st, err := store.Open(filepath.Join(t.TempDir(), "accounts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s.Store = st
	a, err := st.CreateUser("alice", "unused")
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.CreateUser("bob", "unused")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateSession("bob-session", b.ID, time.Hour); err != nil {
		t.Fatal(err)
	}
	called := false
	h := s.authed(func(w http.ResponseWriter, r *http.Request, u *store.User) { called = true; w.WriteHeader(204) })
	r := httptest.NewRequest("POST", "/api/history", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "bob-session"})
	r.Header.Set("X-TBMusic-User-ID", strconv.FormatInt(a.ID, 10))
	w := httptest.NewRecorder()
	h(w, r)
	if w.Code != 409 || called || !strings.Contains(w.Body.String(), "account_changed") {
		t.Fatalf("wrong account write: %d %s", w.Code, w.Body.String())
	}
	r.Header.Set("X-TBMusic-User-ID", strconv.FormatInt(b.ID, 10))
	w = httptest.NewRecorder()
	h(w, r)
	if w.Code != 204 || !called {
		t.Fatalf("matching account rejected: %d", w.Code)
	}
}

func TestPlaybackCacheSeparatesResolverInputs(t *testing.T) {
	s := testCachedServer(t)
	forged := music.Song{Source: "wy", Songmid: "same", SongID: "other"}
	honest := forged
	honest.SongID = "same"
	encoded, _ := json.Marshal(resolvedURL{URL: "https://8.8.8.8/other.mp3", Quality: "320k"})
	if err := s.Cache.Set(requestKey("url", "wy", cacheSongKey(&forged), "320k"), encoded, time.Hour, ""); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(musicReq{Source: "wy", Quality: "320k", MusicInfo: honest})
	w := httptest.NewRecorder()
	s.handleMusicURL(w, httptest.NewRequest("POST", "/api/music-url", strings.NewReader(string(body))), &store.User{ID: 992})
	if w.Code != 503 || strings.Contains(w.Body.String(), "other.mp3") {
		t.Fatalf("cache collision: %d %s", w.Code, w.Body.String())
	}
}

func TestMiguTransientFailuresAreNotCachedAsLyrics(t *testing.T) {
	for _, networkFailure := range []bool{false, true} {
		t.Run(fmt.Sprint(networkFailure), func(t *testing.T) {
			s := testCachedServer(t)
			s.Sources["mg"] = mg.Source{}
			old := music.SafeHTTPClient
			defer func() { music.SafeHTTPClient = old }()
			calls := 0
			music.SafeHTTPClient = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				status, body := 200, "[00:01]good"
				if calls == 1 {
					if networkFailure {
						return nil, errors.New("temporary failure")
					}
					status, body = 500, "upstream error"
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})}
			for i, want := range []int{502, 200, 200} {
				w := httptest.NewRecorder()
				s.handleLyric(w, httptest.NewRequest("POST", "/api/lyric", strings.NewReader(`{"source":"mg","musicInfo":{"source":"mg","songmid":"x","lrcUrl":"https://8.8.8.8/lyric"}}`)), nil)
				if w.Code != want {
					t.Fatalf("request %d: %d %s", i, w.Code, w.Body.String())
				}
			}
			if calls != 2 {
				t.Fatalf("upstream calls=%d", calls)
			}
		})
	}
}

func TestLastSupportedPageHasNoMore(t *testing.T) {
	p := &music.Page[music.Song]{Page: 100, HasMore: true}
	if _, err := pageResult(p, nil); err != nil || p.HasMore {
		t.Fatal("last page advertises another page")
	}
}
