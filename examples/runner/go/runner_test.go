package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestVerifySignature(t *testing.T) {
	body := []byte(`{"event":"comment.created"}`)
	secret := "shh"
	good := sign(secret, body)

	tests := []struct {
		name   string
		secret string
		body   []byte
		header string
		want   bool
	}{
		{"valid passes", secret, body, good, true},
		{"tampered body fails", secret, []byte(`{"event":"x"}`), good, false},
		{"wrong signature fails", secret, body, "sha256=deadbeef", false},
		{"missing signature fails", secret, body, "", false},
		{"no secret accepts unsigned", "", body, "", true},
		{"no secret ignores any header", "", body, "sha256=whatever", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := verifySignature(tt.secret, tt.body, tt.header); got != tt.want {
				t.Fatalf("verifySignature = %v, want %v", got, tt.want)
			}
		})
	}
}

// stubBackend records how many times Run was called.
type stubBackend struct{ calls int32 }

func (b *stubBackend) Run() error { atomic.AddInt32(&b.calls, 1); return nil }

func postEvent(t *testing.T, h http.Handler, cfg Config, event string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body)))
	req.Header.Set("X-Outbox-Event", event)
	if cfg.Secret != "" {
		req.Header.Set("X-Outbox-Signature", sign(cfg.Secret, body))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestEventFiltering(t *testing.T) {
	cfg := Config{
		Events:   parseEvents("comment.created,comment.replied"),
		Debounce: 5 * time.Millisecond,
	}
	backend := &stubBackend{}
	srv := NewServer(cfg, backend)
	h := srv.Handler()

	// Allowed event → accepted (202) and the agent eventually runs.
	rec := postEvent(t, h, cfg, "comment.created", []byte(`{"event":"comment.created"}`))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("allowed event: code = %d, want 202", rec.Code)
	}

	// Filtered event → 200 ignore, no extra run.
	rec = postEvent(t, h, cfg, "comment.resolved", []byte(`{"event":"comment.resolved"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("filtered event: code = %d, want 200", rec.Code)
	}

	time.Sleep(40 * time.Millisecond)
	if got := atomic.LoadInt32(&backend.calls); got != 1 {
		t.Fatalf("agent calls = %d, want 1 (only the allowed event)", got)
	}
}

func TestUnauthorizedWhenSecretSet(t *testing.T) {
	cfg := Config{
		Secret:   "shh",
		Events:   parseEvents("comment.created"),
		Debounce: 5 * time.Millisecond,
	}
	backend := &stubBackend{}
	h := NewServer(cfg, backend).Handler()

	body := []byte(`{"event":"comment.created"}`)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body)))
	req.Header.Set("X-Outbox-Event", "comment.created")
	req.Header.Set("X-Outbox-Signature", "sha256=bad")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad signature: code = %d, want 401", rec.Code)
	}
	time.Sleep(20 * time.Millisecond)
	if got := atomic.LoadInt32(&backend.calls); got != 0 {
		t.Fatalf("agent calls = %d, want 0 (rejected before run)", got)
	}
}

func TestDebounceCoalesces(t *testing.T) {
	var calls int32
	r := NewRunner(30*time.Millisecond, func() { atomic.AddInt32(&calls, 1) })

	// Burst of triggers within the window → exactly one run.
	for i := 0; i < 10; i++ {
		r.Trigger()
		time.Sleep(2 * time.Millisecond)
	}
	time.Sleep(80 * time.Millisecond)
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("debounce: calls = %d, want 1", got)
	}
}

func TestSingleFlightRerun(t *testing.T) {
	var calls int32
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	r := NewRunner(time.Millisecond, func() {
		atomic.AddInt32(&calls, 1)
		select {
		case started <- struct{}{}:
		default:
		}
		<-release // block the first run so we can pile up triggers
	})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); r.execute() }() // run #1 starts and blocks
	<-started

	// While run #1 is in flight, fire several more executes: they must NOT start
	// a concurrent run; they collapse into exactly ONE pending rerun.
	for i := 0; i < 5; i++ {
		r.execute()
	}

	close(release) // let run #1 finish; the drain loop performs run #2
	wg.Wait()

	// Give the rerun (same goroutine loop) a moment; it does not block now.
	time.Sleep(20 * time.Millisecond)
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("single-flight: calls = %d, want 2 (initial + one coalesced rerun)", got)
	}
}

func TestBuildArgs(t *testing.T) {
	args := buildArgs("claude -p {prompt} --allowedTools mcp__outbox-md__*", "do the thing now")
	want := []string{"claude", "-p", "do the thing now", "--allowedTools", "mcp__outbox-md__*"}
	if len(args) != len(want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("args[%d] = %q, want %q", i, args[i], want[i])
		}
	}
}
