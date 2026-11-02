package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tbmusic/internal/config"
	"tbmusic/internal/store"
)

func TestCacheCapacityPersistsAndRollsBackOnSaveFailure(t *testing.T) {
	s := testCachedServer(t)
	s.Cfg.SetCacheSizeMB(1)
	s.ConfigPath = filepath.Join(t.TempDir(), "config.yaml")
	put := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.handleResizeCache(w, httptest.NewRequest("PUT", "/api/admin/cache", strings.NewReader(body)), nil)
		return w
	}
	if w := put(`{"sizeMB":4096}`); w.Code != 200 {
		t.Fatalf("resize: %d %s", w.Code, w.Body.String())
	}
	loaded, err := config.Load(s.ConfigPath)
	if err != nil || loaded.GetCacheSizeMB() != 4096 || s.Cache.Stats().Limit != 4<<30 {
		t.Fatalf("capacity was not saved/applied: %v", err)
	}
	if hosts := loaded.GetStreamHosts(); len(hosts) != 1 || hosts[0] != "8.8.8.8" {
		t.Fatal("saving capacity changed other settings")
	}
	for _, body := range []string{`{}`, `{"sizeMB":0}`, `{"sizeMB":-1}`, `{"sizeMB":1.5}`, `{"sizeMB":1048577}`, `{"sizeMB":"bad"}`} {
		if w := put(body); w.Code != 400 {
			t.Fatalf("invalid capacity accepted: %s", body)
		}
	}
	s.ConfigPath = filepath.Join(t.TempDir(), "missing", "config.yaml")
	if w := put(`{"sizeMB":2}`); w.Code != 500 {
		t.Fatal("save failure was not reported")
	}
	if s.Cfg.GetCacheSizeMB() != 4096 || s.Cache.Stats().Limit != 4<<30 {
		t.Fatal("save failure left a different runtime capacity")
	}
}

func TestClearCacheDiscardsInFlightLoads(t *testing.T) {
	s := testCachedServer(t)
	if err := s.Cache.Set("cover|existing", []byte("image"), time.Hour, ""); err != nil {
		t.Fatal(err)
	}
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		b, err := s.cached(context.Background(), "search|pending", time.Hour, func() ([]byte, error) {
			close(started)
			<-release
			return []byte("result"), nil
		})
		if err != nil || string(b) != "result" {
			t.Errorf("in-flight response interrupted: %s %v", b, err)
		}
	}()
	<-started
	w := httptest.NewRecorder()
	s.handleClearCache(w, httptest.NewRequest("DELETE", "/api/admin/cache", nil), nil)
	close(release)
	<-done
	if w.Code != 200 || s.Cache.Stats().Entries != 0 || s.Cache.Stats().Bytes != 0 {
		t.Fatalf("cache repopulated by an old request: %d %+v", w.Code, s.Cache.Stats())
	}
	_, err := s.cached(context.Background(), "search|fresh", time.Hour, func() ([]byte, error) { return []byte("fresh"), nil })
	if err != nil || s.Cache.Stats().Entries != 1 {
		t.Fatal("new requests failed to cache")
	}
}

func TestCacheManagementRequiresAdmin(t *testing.T) {
	s := testCachedServer(t)
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s.Store = st
	u, err := st.CreateUser("listener", "unused-hash")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateSession("listener-session", u.ID, time.Hour); err != nil {
		t.Fatal(err)
	}
	router := s.Router()
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		for _, signedIn := range []bool{false, true} {
			r := httptest.NewRequest(method, "/api/admin/cache", strings.NewReader(`{"sizeMB":2}`))
			want := http.StatusUnauthorized
			if signedIn {
				r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "listener-session"})
				want = http.StatusForbidden
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			if w.Code != want {
				t.Fatalf("%s signedIn=%v: got %d want %d", method, signedIn, w.Code, want)
			}
		}
	}
}
