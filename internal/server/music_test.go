package server

import (
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"tbmusic/internal/config"
	"tbmusic/internal/music"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestStreamDownload(t *testing.T) {
	// No external network: a public literal IP passes SSRF checks, while the
	// transport supplies deterministic audio. Production validation is unchanged.
	cfg := config.Default()
	cfg.SetStreamHosts([]string{"8.8.8.8"})
	srv := testCachedServer(t)
	original := music.SafeHTTPClient
	music.SafeHTTPClient = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200, Header: http.Header{"Content-Type": {"audio/flac"}},
			Body: io.NopCloser(strings.NewReader("test-audio")), ContentLength: 10,
		}, nil
	})}
	t.Cleanup(func() { music.SafeHTTPClient = original })
	for _, download := range []bool{false, true} {
		query := url.Values{"source": {"wy"}, "url": {"https://8.8.8.8/song.flac"}}
		if download {
			query.Set("download", "中文歌曲/测试\r\n")
		}
		w := httptest.NewRecorder()
		srv.handleStream(w, httptest.NewRequest("GET", "/api/stream?"+query.Encode(), nil), nil)
		if w.Code != 200 || w.Body.String() != "test-audio" || !w.Flushed {
			t.Fatalf("stream failed: status=%d body=%q flushed=%v", w.Code, w.Body.String(), w.Flushed)
		}
		disposition := w.Header().Get("Content-Disposition")
		if !download {
			if disposition != "" {
				t.Fatalf("playback unexpectedly became a download: %s", disposition)
			}
			continue
		}
		kind, params, err := mime.ParseMediaType(disposition)
		if err != nil || kind != "attachment" || params["filename"] != "中文歌曲_测试__.flac" {
			t.Fatalf("invalid download header: %q %v", disposition, err)
		}
	}
	if srv.Cache.Stats().Entries != 0 {
		t.Fatal("audio bytes must never enter the disk cache")
	}
}

func TestDownloadFilename(t *testing.T) {
	for _, tc := range []struct{ name, url, mime, want string }{
		{"歌名", "https://example.com/track.MP3?token=x", "audio/mpeg", "歌名.mp3"},
		{"歌名", "https://example.com/audio", "audio/flac; charset=binary", "歌名.flac"},
		{"..", "https://example.com/audio.html", "audio/mp4", "未命名.m4a"},
		{"a/b\\c\n", "https://example.com/audio", "application/octet-stream", "a_b_c_.mp3"},
	} {
		if got := downloadFilename(tc.name, tc.url, tc.mime); got != tc.want {
			t.Errorf("filename = %q, want %q", got, tc.want)
		}
	}
}
