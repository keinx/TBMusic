package music

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"
)

type contextTransport func(*http.Request) (*http.Response, error)

func (f contextTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestMetadataHTTPHonorsCancellation(t *testing.T) {
	old := Client
	defer func() { Client = old }()
	entered := make(chan struct{})
	Client = &http.Client{Transport: contextTransport(func(r *http.Request) (*http.Response, error) {
		close(entered)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, _, err := GetString(ctx, "https://example.invalid", nil); done <- err }()
	<-entered
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("request survived cancellation")
	}
	if len(metadataHTTPSem) != 0 {
		t.Fatal("HTTP slot leaked")
	}
	for i := 0; i < cap(metadataHTTPSem); i++ {
		metadataHTTPSem <- struct{}{}
	}
	defer func() {
		for len(metadataHTTPSem) > 0 {
			<-metadataHTTPSem
		}
	}()
	if _, _, err := GetString(context.Background(), "https://example.invalid", nil); !errors.Is(err, ErrBusy) {
		t.Fatalf("admission: %v", err)
	}
}

func TestDNSVerdictsHaveHardCapacityAndRespectCanceledContext(t *testing.T) {
	dnsGuard.mu.Lock()
	dnsGuard.m = nil
	dnsGuard.order.Init()
	dnsGuard.mu.Unlock()
	defer func() { dnsGuard.mu.Lock(); dnsGuard.m = nil; dnsGuard.order.Init(); dnsGuard.mu.Unlock() }()
	for i := 0; i < dnsGuardCapacity+100; i++ {
		if err := lookupHostVerdict(fmt.Sprintf("11.0.%d.%d", i/250, i%250+1)); err != nil {
			t.Fatal(err)
		}
	}
	dnsGuard.mu.Lock()
	size, ordered := len(dnsGuard.m), dnsGuard.order.Len()
	dnsGuard.mu.Unlock()
	if size != dnsGuardCapacity || ordered != size {
		t.Fatalf("cache=%d list=%d", size, ordered)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := SafeOutboundURLContext(ctx, "https://8.8.8.8"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled DNS: %v", err)
	}
}
