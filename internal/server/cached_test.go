package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"tbmusic/internal/cache"
	"tbmusic/internal/config"
	"tbmusic/internal/music"
	"tbmusic/internal/store"
)

type countingSource struct {
	music.Base
	search, details, lyrics, pics atomic.Int32
	fail                          bool
}

func (*countingSource) ID() string   { return "wy" }
func (*countingSource) Name() string { return "test" }
func (s *countingSource) SearchMusic(ctx context.Context, q string, p, n int) (*music.Page[music.Song], error) {
	s.search.Add(1)
	if s.fail {
		return nil, errors.New("upstream failed")
	}
	return &music.Page[music.Song]{List: []music.Song{{Source: "wy", Songmid: q, Name: q}}, Page: p, Limit: n}, nil
}
func (s *countingSource) SonglistSongs(ctx context.Context, id string, p, n int) (*music.Page[music.Song], error) {
	s.details.Add(1)
	return &music.Page[music.Song]{List: []music.Song{{Name: id}}, Page: p, Limit: n}, nil
}
func (s *countingSource) GetLyric(context.Context, *music.Song) (*music.Lyrics, error) {
	s.lyrics.Add(1)
	return &music.Lyrics{Lyric: "[00:01]test"}, nil
}
func (s *countingSource) GetPic(context.Context, *music.Song) (string, error) {
	s.pics.Add(1)
	return "https://8.8.8.8/cover.png", nil
}
func testCachedServer(t *testing.T) *Server {
	t.Helper()
	cfg := config.Default()
	cfg.SetStreamHosts([]string{"8.8.8.8"})
	s := New(nil, cfg, "", nil)
	var err error
	s.Cache, err = cache.Open(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func TestMetadataCachingAndKeys(t *testing.T) {
	s := testCachedServer(t)
	source := &countingSource{}
	s.Sources["wy"] = source
	get := func(path string, handler func(http.ResponseWriter, *http.Request, *store.User)) []byte {
		t.Helper()
		w := httptest.NewRecorder()
		handler(w, httptest.NewRequest("GET", path, nil), nil)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	path := "/api/search?source=wy&type=music&keyword=test&page=1&limit=30"
	first := get(path, s.handleSearch)
	second := get(path, s.handleSearch)
	if !bytes.Equal(first, second) || source.search.Load() != 1 {
		t.Fatal("search cache missed")
	}
	get(path+"&unused=x", s.handleSearch)
	get(strings.Replace(path, "page=1", "page=2", 1), s.handleSearch)
	get(strings.Replace(path, "keyword=test", "keyword=other", 1), s.handleSearch)
	if source.search.Load() != 3 {
		t.Fatal("search key omits pagination/keyword or includes irrelevant query")
	}
	path = "/api/detail?source=wy&type=songlist&id=123&page=1&limit=30"
	get(path, s.handleDetail)
	get(path, s.handleDetail)
	if source.details.Load() != 1 {
		t.Fatal("songlist cache missed")
	}
	body := `{"source":"wy","musicInfo":{"source":"wy","songmid":"a","name":"test"}}`
	for _, handler := range []func(http.ResponseWriter, *http.Request, *store.User){s.handleLyric, s.handlePic} {
		for i := 0; i < 2; i++ {
			w := httptest.NewRecorder()
			handler(w, httptest.NewRequest("POST", "/api/test", strings.NewReader(body)), nil)
			if w.Code != 200 {
				t.Fatalf("metadata: %d %s", w.Code, w.Body.String())
			}
		}
	}
	if source.lyrics.Load() != 1 || source.pics.Load() != 1 {
		t.Fatal("lyric/pic cache missed")
	}
	source.fail = true
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		s.handleSearch(w, httptest.NewRequest("GET", "/api/search?source=wy&keyword=failed", nil), nil)
		if w.Code != 502 {
			t.Fatal("failed result cached as success")
		}
	}
	if source.search.Load() != 5 {
		t.Fatal("failure was cached")
	}
}
func TestCachedURLWorksBeforeScriptsReadyAndCanBeInvalidated(t *testing.T) {
	s := testCachedServer(t)
	song := music.Song{Source: "wy", Songmid: "id"}
	key := requestKey("url", "wy", cacheSongKey(&song), "320k")
	result := resolvedURL{URL: "https://8.8.8.8/audio.mp3", Quality: "128k", Script: "test"}
	b, _ := json.Marshal(result)
	if err := s.Cache.Set(key, b, time.Hour, requestKey("url-tag", result.URL)); err != nil {
		t.Fatal(err)
	}
	request, _ := json.Marshal(musicReq{Source: "wy", Quality: "320k", MusicInfo: song})
	run := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.handleMusicURL(w, httptest.NewRequest("POST", "/api/music-url", bytes.NewReader(request)), &store.User{ID: 99})
		return w
	}
	w := run()
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"cached":true`) || !strings.Contains(w.Body.String(), `"quality":"128k"`) {
		t.Fatalf("cached fallback quality not reused: %d %s", w.Code, w.Body.String())
	}
	s.Cache.DeleteTag(requestKey("url-tag", result.URL))
	if w = run(); w.Code != 503 {
		t.Fatalf("stale URL survived invalidation: %d", w.Code)
	}
}
func TestCoverCachesImageBytesAndRejectsNonImages(t *testing.T) {
	s := testCachedServer(t)
	original := music.SafeHTTPClient
	t.Cleanup(func() { music.SafeHTTPClient = original })
	var calls atomic.Int32
	image := []byte("\x89PNG\r\n\x1a\nimage-data")
	music.SafeHTTPClient = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		b := image
		if strings.HasSuffix(r.URL.Path, ".html") {
			b = []byte("<html>not a cover</html>")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(b))}, nil
	})}
	get := func(raw string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.handleCover(w, httptest.NewRequest("GET", "/api/cover?url="+url.QueryEscape(raw), nil), nil)
		return w
	}
	for i := 0; i < 2; i++ {
		w := get("https://8.8.8.8/cover.png")
		if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), image) {
			t.Fatalf("cover: %d %s", w.Code, w.Body.String())
		}
	}
	if calls.Load() != 1 || s.Cache.Stats().Categories["cover"] != 1 {
		t.Fatal("cover bytes were not cached")
	}
	if w := get("https://8.8.8.8/cover.html"); w.Code != 502 {
		t.Fatal("accepted non-image")
	}
	if w := get("http://127.0.0.1/private.png"); w.Code != 502 {
		t.Fatal("allowed private-network image proxy")
	}
	if s.Cache.Stats().Categories["cover"] != 1 {
		t.Fatal("bad response polluted cache")
	}
}
func TestConcurrentMissesShareOneUpstreamRequest(t *testing.T) {
	s := testCachedServer(t)
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	var wg sync.WaitGroup
	loader := func() ([]byte, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return []byte("result"), nil
	}
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b, err := s.cached(context.Background(), "search|same", time.Hour, loader)
			if err != nil || string(b) != "result" {
				t.Errorf("load: %s %v", b, err)
			}
		}()
	}
	<-started
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("upstream called %d times", calls.Load())
	}
}
