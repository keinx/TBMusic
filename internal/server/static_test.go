package server

import (
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestReleaseManifestAndMissingAssetsDoNotUseStaleHTML(t *testing.T) {
	s := &Server{StaticFS: fstest.MapFS{
		"index.html":         {Data: []byte("<html>app</html>")},
		"version.json":       {Data: []byte(`{"version":"1.0.0","buildId":"build-b","builtAt":"2026-10-31T00:00:00Z"}`)},
		"assets/app-hash.js": {Data: []byte("console.log('app')")},
	}}
	for _, tc := range []struct {
		path, cache, contains string
		status                int
	}{
		{"/version.json?t=123", "no-store", `"buildId":"build-b"`, 200},
		{"/settings", "no-cache", "<html>app</html>", 200},
		{"/assets/app-hash.js", "public, max-age=31536000, immutable", "console.log", 200},
		{"/assets/deleted-build.js", "no-store", "资源不存在", 404},
	} {
		w := httptest.NewRecorder()
		s.staticHandler().ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
		if w.Code != tc.status || w.Header().Get("Cache-Control") != tc.cache || !strings.Contains(w.Body.String(), tc.contains) {
			t.Fatalf("%s: %d %v %s", tc.path, w.Code, w.Header(), w.Body.String())
		}
	}
	delete(s.StaticFS.(fstest.MapFS), "version.json")
	w := httptest.NewRecorder()
	s.staticHandler().ServeHTTP(w, httptest.NewRequest("GET", "/version.json", nil))
	if w.Code != 404 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("missing release manifest fell back to cacheable SPA HTML")
	}
}
