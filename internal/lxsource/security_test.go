package lxsource

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dop251/goja"
)

func cachedTestManager(t *testing.T, script string) *Manager {
	t.Helper()
	old := scriptCacheDir
	scriptCacheDir = t.TempDir()
	t.Cleanup(func() { scriptCacheDir = old })
	m := NewManager("test", "https://example.invalid/source.js", time.Second)
	m.writeCache(script)
	t.Cleanup(m.Stop)
	if err := m.Load(); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestRequireCannotReadLocalJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private.json")
	if err := os.WriteFile(path, []byte(`{"secret":"must-stay-local"}`), 0600); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(filepath.ToSlash(path))
	m := cachedTestManager(t, `let blocked=false; try { require(`+string(encoded)+`); } catch(e) { blocked=true; }
	if (!blocked) throw new Error('local file was readable');
	lx.on('request', () => Promise.resolve('https://8.8.8.8/audio'));
	lx.send('inited', {status:true});`)
	if _, err := m.MusicURL(context.Background(), "wy", "128k", nil); err != nil {
		t.Fatal(err)
	}
}

func TestCallsDoNotRetainPerRequestGlobals(t *testing.T) {
	m := cachedTestManager(t, `lx.on('request', () => Promise.resolve('https://8.8.8.8/audio')); lx.send('inited',{status:true});`)
	for i := 0; i < 100; i++ {
		if _, err := m.MusicURL(context.Background(), "wy", "128k", nil); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan []string, 1)
	m.runInLoop(func(vm *goja.Runtime) { done <- vm.GlobalObject().GetOwnPropertyNames() })
	for _, key := range <-done {
		if strings.HasPrefix(key, "__lxResult_") || strings.HasPrefix(key, "__lxSettle_") {
			t.Fatalf("leaked %s", key)
		}
	}
}

func TestStopInterruptsBusyScriptAndTerminatesTimers(t *testing.T) {
	m := cachedTestManager(t, `setInterval(() => {}, 1000); lx.send('inited',{status:true});`)
	started := make(chan struct{})
	m.runInLoop(func(vm *goja.Runtime) { close(started); _, _ = vm.RunString(`while(true){}`) })
	<-started
	done := make(chan struct{})
	go func() { m.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop hung on JavaScript loop")
	}
	if m.Ready() || m.loop.RunOnLoop(func(*goja.Runtime) {}) {
		t.Fatal("stopped event loop still accepts work")
	}
}

func TestStopCancelsPendingLoad(t *testing.T) {
	m := NewManager("test", "https://example.invalid/source.js", time.Second)
	m.Stop()
	if _, err := m.download(); err == nil {
		t.Fatal("stopped manager attempted download")
	}
}
