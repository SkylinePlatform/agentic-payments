package main

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/collector"
)

// freeAddr asks the kernel for a port nobody is using and gives it straight
// back. A fixed port would make this test fail whenever a real collector — or
// another test — happened to be running.
func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return addr
}

// TestRunServesAndShutsDown is the wiring test: the flags parse, the server
// binds, it answers, and cancelling the context returns from run rather than
// hanging on an SSE stream that never ends on its own.
func TestRunServesAndShutsDown(t *testing.T) {
	addr := freeAddr(t)
	ctx, cancel := context.WithCancel(context.Background())

	var stderr bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- run(ctx, []string{"-addr", addr}, &stderr) }()

	// Wait for the listener by dialling it, rather than by sleeping.
	var resp *http.Response
	for range 200 {
		r, err := http.Get("http://" + addr + "/healthz")
		if err == nil {
			resp = r
			break
		}
	}
	if resp == nil {
		cancel()
		t.Fatal("the server never came up")
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("healthz = %d, want 200", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// A stream is open across the shutdown, which is the case that deadlocks
	// if the hub is not closed before Shutdown waits for outstanding requests.
	streamReq, err := http.NewRequestWithContext(context.Background(),
		http.MethodGet, "http://"+addr+collector.EventsPath, nil)
	if err != nil {
		cancel()
		t.Fatalf("NewRequest: %v", err)
	}
	stream, err := http.DefaultClient.Do(streamReq)
	if err != nil {
		cancel()
		t.Fatalf("open stream: %v", err)
	}
	defer func() { _ = stream.Body.Close() }()

	cancel()
	if err := <-done; err != nil {
		t.Errorf("run returned %v, want nil", err)
	}
	if !strings.Contains(stderr.String(), "not an AP2 role") {
		t.Errorf("the startup line does not say what this binary is: %q", stderr.String())
	}
}

func TestRunRejectsBadFlags(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer
	if err := run(context.Background(), []string{"-nonsense"}, &stderr); err == nil {
		t.Error("an unknown flag was accepted")
	}
	if err := run(context.Background(), []string{"-history", "-1"}, &stderr); err == nil {
		t.Error("a negative history was accepted")
	}
}

func TestRunReportsAnUnusableAddress(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer
	err := run(context.Background(), []string{"-addr", "256.256.256.256:99999"}, &stderr)
	if err == nil {
		t.Fatal("an unbindable address was accepted")
	}
	if errors.Is(err, http.ErrServerClosed) {
		t.Error("a bind failure was reported as a clean shutdown")
	}
}
