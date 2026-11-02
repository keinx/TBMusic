package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"tbmusic/internal/music"
	"tbmusic/internal/store"
)

func TestCrossSiteWritesAndPlainTextJSONAreRejected(t *testing.T) {
	s := testCachedServer(t)
	router := s.Router()
	for _, headers := range []http.Header{
		{"Sec-Fetch-Site": {"cross-site"}}, {"Origin": {"https://attacker.example"}},
		{"Sec-Fetch-Site": {"same-site"}, "Origin": {"https://sibling.example.com"}},
	} {
		r := httptest.NewRequest("POST", "http://example.com/api/auth/login", strings.NewReader(`{"username":"attacker","password":"secret"}`))
		r.Header = headers
		r.Header.Set("Content-Type", "text/plain")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != 403 || len(w.Result().Cookies()) != 0 {
			t.Fatalf("cross-origin write accepted: %d", w.Code)
		}
	}
	for _, tc := range []struct {
		content, body string
		valid         bool
	}{
		{"text/plain", `{"value":1}`, false}, {"application/x-www-form-urlencoded", `{"value":1}`, false},
		{"application/json", `{"value":1}{"value":2}`, false}, {"application/json; charset=utf-8", `{"value":1}`, true},
	} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(tc.body))
		r.Header.Set("Content-Type", tc.content)
		var data map[string]any
		if err := decodeBody(httptest.NewRecorder(), r, &data); (err == nil) != tc.valid {
			t.Fatalf("%s %s: %v", tc.content, tc.body, err)
		}
	}
}

func TestStaticOnlyServesReadMethods(t *testing.T) {
	s := &Server{StaticFS: fstest.MapFS{"index.html": {Data: []byte("app")}}}
	for _, method := range []string{"CONNECT", "POST", "PUT", "DELETE", "TRACE"} {
		w := httptest.NewRecorder()
		s.staticHandler().ServeHTTP(w, httptest.NewRequest(method, "http://example.com/", nil))
		if w.Code != 405 || w.Header().Get("Allow") != "GET, HEAD" {
			t.Fatalf("%s accepted: %d", method, w.Code)
		}
	}
}

func TestStreamRedirectCannotEscapeAllowlist(t *testing.T) {
	s := testCachedServer(t)
	old := music.SafeHTTPClient
	defer func() { music.SafeHTTPClient = old }()
	requests := 0
	music.SafeHTTPClient = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"https://1.1.1.1/private"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})}
	w := httptest.NewRecorder()
	s.handleStream(w, httptest.NewRequest("GET", "/api/stream?url="+url.QueryEscape("https://8.8.8.8/song"), nil), nil)
	if w.Code != 502 || requests != 1 || len(s.streamSem) != 0 {
		t.Fatalf("redirect escaped: status=%d requests=%d slots=%d", w.Code, requests, len(s.streamSem))
	}
}

type deadlineWriter struct {
	*httptest.ResponseRecorder
	deadline       time.Time
	protectedWrite bool
}

func (w *deadlineWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	return nil
}
func (w *deadlineWriter) Write(p []byte) (int, error) {
	if w.deadline.IsZero() {
		return 0, errors.New("write without deadline")
	}
	w.protectedWrite = true
	return 0, errors.New("simulated slow client timeout")
}
func TestStreamAppliesDownstreamDeadlineAndReleasesSlot(t *testing.T) {
	s := testCachedServer(t)
	old := music.SafeHTTPClient
	defer func() { music.SafeHTTPClient = old }()
	music.SafeHTTPClient = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("audio")), ContentLength: 5}, nil
	})}
	w := &deadlineWriter{ResponseRecorder: httptest.NewRecorder()}
	s.handleStream(w, httptest.NewRequest("GET", "/api/stream?url="+url.QueryEscape("https://8.8.8.8/song"), nil), nil)
	if len(s.streamSem) != 0 {
		t.Fatal("slow client leaked stream slot")
	}
	if !w.protectedWrite {
		t.Fatal("stream write had no downstream deadline")
	}
	// ResponseController's deadline is reset when the handler completes.
	if !w.deadline.IsZero() {
		t.Fatal("deadline leaked to next request")
	}
}

type suppliedPicSource struct{ countingSource }

func (*suppliedPicSource) GetPic(_ context.Context, song *music.Song) (string, error) {
	return song.Pic, nil
}
func TestCoverCacheSeparatesCallerSuppliedPictures(t *testing.T) {
	s := testCachedServer(t)
	s.Sources["wy"] = &suppliedPicSource{}
	for _, pic := range []string{"https://8.8.8.8/attacker", "https://8.8.8.8/real"} {
		w := httptest.NewRecorder()
		s.handlePic(w, httptest.NewRequest("POST", "/api/pic", strings.NewReader(`{"source":"wy","musicInfo":{"songmid":"same","pic":"`+pic+`"}}`)), nil)
		if w.Code != 200 || !strings.Contains(w.Body.String(), pic) {
			t.Fatalf("cover cache poisoning: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestLogoutDatabaseErrorsAreNotReportedAsSuccess(t *testing.T) {
	s := testCachedServer(t)
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	s.Store = st
	st.Close()
	r := httptest.NewRequest("POST", "/api/auth/logout", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "existing"})
	w := httptest.NewRecorder()
	s.handleLogout(w, r, nil)
	if w.Code != 500 || len(w.Result().Cookies()) != 0 {
		t.Fatalf("logout falsely succeeded: %d", w.Code)
	}
	w = httptest.NewRecorder()
	s.Router().ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatalf("database failure confused with expired session: %d", w.Code)
	}
}
