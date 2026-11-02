package lxsource

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type scopeTransport func(*http.Request) (*http.Response, error)

func (f scopeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSendGetterCanReenterHost(t *testing.T) {
	m := cachedTestManager(t, `lx.send('inited',{get status(){lx.on('request',()=>Promise.resolve('https://8.8.8.8/audio'));return true}});`)
	if _, err := m.MusicURL(context.Background(), "wy", "128k", nil); err != nil {
		t.Fatal(err)
	}
}

func TestRequestsAreBoundedAndCanceledWithCall(t *testing.T) {
	old := jsHTTPClient
	defer func() { jsHTTPClient = old }()
	seen := make(chan *http.Request, 16)
	finished := make(chan struct{}, 16)
	jsHTTPClient = &http.Client{Transport: scopeTransport(func(r *http.Request) (*http.Response, error) {
		seen <- r
		<-r.Context().Done()
		finished <- struct{}{}
		return nil, r.Context().Err()
	})}
	m := cachedTestManager(t, `lx.on('request',()=>new Promise(()=>{for(let i=0;i<16;i++){try{lx.request('https://8.8.8.8/a',{timeout:60000},()=>{})}catch{}}}));lx.send('inited',{status:true});`)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := m.MusicURL(ctx, "wy", "128k", nil); done <- err }()
	for i := 0; i < cap(m.httpSem); i++ {
		select {
		case <-seen:
		case <-time.After(time.Second):
			t.Fatal("request did not start")
		}
	}
	if len(seen) != 0 {
		t.Fatal("per-script limit exceeded")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled call succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("call did not stop")
	}
	for i := 0; i < cap(m.httpSem); i++ {
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Fatal("HTTP survived cancellation")
		}
	}
}

type brokenScopeBody struct{ first bool }

func (b *brokenScopeBody) Read(p []byte) (int, error) {
	if !b.first {
		b.first = true
		return copy(p, "partial"), nil
	}
	return 0, io.ErrUnexpectedEOF
}
func (*brokenScopeBody) Close() error { return nil }

func TestTruncatedResponseReachesErrorCallback(t *testing.T) {
	old := jsHTTPClient
	defer func() { jsHTTPClient = old }()
	jsHTTPClient = &http.Client{Transport: scopeTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: &brokenScopeBody{}, Request: r}, nil
	})}
	m := cachedTestManager(t, `lx.on('request',()=>new Promise(resolve=>lx.request('https://8.8.8.8/a',{},(err)=>resolve('https://8.8.8.8/'+(err?'error':'success')))));lx.send('inited',{status:true});`)
	u, err := m.MusicURL(context.Background(), "wy", "128k", nil)
	if err != nil || !strings.HasSuffix(u, "/error") {
		t.Fatalf("%s %v", u, err)
	}
}

func TestResponseCallbackCanContinueAtConcurrencyLimit(t *testing.T) {
	old := jsHTTPClient
	defer func() { jsHTTPClient = old }()
	ready := make(chan struct{})
	var first atomic.Int32
	jsHTTPClient = &http.Client{Transport: scopeTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/first" {
			if first.Add(1) == 4 {
				close(ready)
			}
			select {
			case <-ready:
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok")), Request: r}, nil
	})}
	m := cachedTestManager(t, `lx.on('request',()=>Promise.all(Array.from({length:4},()=>new Promise((resolve,reject)=>lx.request('https://8.8.8.8/first',{},err=>{if(err)return reject(err);try{lx.request('https://8.8.8.8/second',{},err=>err?reject(err):resolve())}catch(e){reject(e)}})))).then(()=>'https://8.8.8.8/audio'));lx.send('inited',{status:true});`)
	if _, err := m.MusicURL(context.Background(), "wy", "128k", nil); err != nil {
		t.Fatal(err)
	}
}

func TestOversizedResponseReachesErrorCallback(t *testing.T) {
	old := jsHTTPClient
	defer func() { jsHTTPClient = old }()
	jsHTTPClient = &http.Client{Transport: scopeTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(strings.Repeat("x", (20<<20)+1))), Request: r}, nil
	})}
	m := cachedTestManager(t, `lx.on('request',()=>new Promise(resolve=>lx.request('https://8.8.8.8/a',{},(err)=>resolve('https://8.8.8.8/'+(err?'error':'success')))));lx.send('inited',{status:true});`)
	u, err := m.MusicURL(context.Background(), "wy", "128k", nil)
	if err != nil || !strings.HasSuffix(u, "/error") {
		t.Fatalf("%s %v", u, err)
	}
}

func TestCanceledCallCannotStartHTTPFromTimer(t *testing.T) {
	old := jsHTTPClient
	defer func() { jsHTTPClient = old }()
	var requests atomic.Int32
	jsHTTPClient = &http.Client{Transport: scopeTransport(func(r *http.Request) (*http.Response, error) {
		requests.Add(1)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok")), Request: r}, nil
	})}
	m := cachedTestManager(t, `lx.on('request',()=>new Promise(()=>setTimeout(()=>lx.request('https://8.8.8.8/a',{},()=>{}),100)));lx.send('inited',{status:true});`)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	m.MusicURL(ctx, "wy", "128k", nil)
	time.Sleep(150 * time.Millisecond)
	if requests.Load() != 0 {
		t.Fatal("canceled timer started HTTP")
	}
}

func TestDownloaderRejectsPrivateTargetsAndRedirects(t *testing.T) {
	old := scriptHTTPClient
	defer func() { scriptHTTPClient = old }()
	client := *old
	requests := 0
	client.Transport = scopeTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"http://127.0.0.1/private"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})
	scriptHTTPClient = &client
	for _, address := range []string{"http://127.0.0.1/script", "https://8.8.8.8/script"} {
		m := NewManager("test", address, time.Second)
		if _, err := m.download(); err == nil {
			t.Fatalf("accepted %s", address)
		}
		m.Stop()
	}
	if requests != 1 {
		t.Fatalf("private transport used: %d", requests)
	}
}

func TestFailedReplacementPreservesRuntimeAndGoodDiskCache(t *testing.T) {
	good := `lx.on('request',()=>Promise.resolve('https://8.8.8.8/audio'));lx.send('inited',{status:true});`
	oldManager := cachedTestManager(t, good)
	oldManager.URL = "https://8.8.8.8/script"
	oldManager.writeCache(good)
	old := scriptHTTPClient
	defer func() { scriptHTTPClient = old }()
	scriptHTTPClient = &http.Client{Transport: scopeTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("invalid javascript !!!")), Request: r}, nil
	})}
	if err := os.Chtimes(oldManager.cachePath(), time.Now().Add(-8*time.Hour), time.Now().Add(-8*time.Hour)); err != nil {
		t.Fatal(err)
	}
	pool := NewPool()
	pool.Set([]*Manager{oldManager})
	defer pool.Close()
	next := NewManager("test", oldManager.URL, time.Second)
	if err := next.Load(); err == nil {
		t.Fatal("invalid script loaded")
	}
	pool.SetPreservingReady([]*Manager{next})
	if pool.Primary() != oldManager || !oldManager.Ready() {
		t.Fatal("working manager discarded")
	}
	if body, ok := next.readCache(false); !ok || body != good {
		t.Fatal("valid cache overwritten")
	}
	if _, err := oldManager.MusicURL(context.Background(), "wy", "128k", nil); err != nil {
		t.Fatal(err)
	}
}
