package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Backend is the pluggable agent driver. cli mode shells out to a coding-agent
// CLI; api mode talks MCP + an LLM directly. Run is called once per debounced
// burst and must be safe to call repeatedly (the runner serializes calls).
type Backend interface {
	Run() error
}

// verifySignature reports whether the request is authentic.
//
//   - secret set, header valid → accept; header missing/wrong → reject.
//   - secret == "" → default-deny: reject unless allowUnsigned is set (an
//     explicit opt-in for the unsigned, no-secret configuration).
//
// The compare is constant-time over the hex digests. body must be the RAW
// request bytes — the same bytes the server signed — read before any parsing.
func verifySignature(secret string, allowUnsigned bool, body []byte, header string) bool {
	if secret == "" {
		return allowUnsigned
	}
	if header == "" {
		return false
	}
	got := strings.TrimPrefix(header, "sha256=")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(got), []byte(want))
}

// Runner debounces incoming webhook triggers and serializes agent runs. A burst
// of triggers within the debounce window collapses into one run; a trigger that
// arrives while a run is in flight schedules exactly one rerun afterwards (so a
// late comment is never dropped, and two agent processes never overlap).
type Runner struct {
	debounce time.Duration
	run      func()

	mu      sync.Mutex
	timer   *time.Timer
	running bool
	pending bool
}

// NewRunner builds a Runner that calls run (the agent invocation) after each
// debounced burst.
func NewRunner(debounce time.Duration, run func()) *Runner {
	return &Runner{debounce: debounce, run: run}
}

// Trigger (re)arms the debounce timer. Repeated calls within the window coalesce
// into a single execute.
func (r *Runner) Trigger() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.timer != nil {
		r.timer.Stop()
	}
	r.timer = time.AfterFunc(r.debounce, func() { go r.execute() })
}

// execute runs the agent with single-flight semantics. If a run is already in
// progress it sets pending and returns; the in-flight loop drains pending and
// runs once more, so concurrent triggers never start a second process.
func (r *Runner) execute() {
	r.mu.Lock()
	if r.running {
		r.pending = true
		r.mu.Unlock()
		return
	}
	r.running = true
	r.mu.Unlock()

	for {
		r.run()
		r.mu.Lock()
		if r.pending {
			r.pending = false
			r.mu.Unlock()
			continue
		}
		r.running = false
		r.mu.Unlock()
		return
	}
}

// Server is the webhook HTTP front end: verify → filter → debounce → run.
type Server struct {
	cfg    Config
	runner *Runner
}

// NewServer wires the verify/filter/debounce front end to the agent backend.
func NewServer(cfg Config, backend Backend) *Server {
	runner := NewRunner(cfg.Debounce, func() {
		log.Printf("runner: invoking agent (mode=%s)", cfg.AgentMode)
		if err := backend.Run(); err != nil {
			log.Printf("runner: agent run failed: %v", err)
			return
		}
		log.Printf("runner: agent run complete")
	})
	return &Server{cfg: cfg, runner: runner}
}

// eventName returns the event name, preferring the header and falling back to
// the JSON body's "event" field.
func eventName(header string, body []byte) string {
	if header != "" {
		return header
	}
	var p struct {
		Event string `json:"event"`
	}
	_ = json.Unmarshal(body, &p)
	return p.Event
}

// handleWebhook is the POST handler mounted at "/". It reads the raw body first,
// verifies the signature over those exact bytes, filters by event, and triggers
// a debounced agent run. It always responds fast and never blocks on the agent.
func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Cap the raw body read BEFORE auth so a large/streamed body cannot OOM the
	// process pre-verification. HMAC still covers the exact bytes read.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.cfg.MaxBodyBytes))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	if !verifySignature(s.cfg.Secret, s.cfg.AllowUnsigned, body, r.Header.Get("X-Outbox-Signature")) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}
	event := eventName(r.Header.Get("X-Outbox-Event"), body)
	if !s.cfg.Events[event] {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ignored\n")
		return
	}
	// Acknowledge receipt to the server BEFORE the debounced spawn so the human's
	// "AI processing…" badge appears within ~1s — even if the agent later dies
	// before it claims. Best-effort and non-fatal: it runs off this goroutine and
	// its failure never blocks or fails webhook handling or the run below.
	s.ackReceived(event, body)
	s.runner.Trigger()
	w.WriteHeader(http.StatusAccepted)
	_, _ = io.WriteString(w, "accepted\n")
}

// commentID pulls the comment id from a webhook payload body. Missing/invalid →
// empty string (the caller then skips the ack).
func commentID(body []byte) string {
	var p struct {
		CommentID string `json:"commentId"`
	}
	_ = json.Unmarshal(body, &p)
	return p.CommentID
}

// ackReceived fires a best-effort, fire-and-forget POST to the server's untokened
// /received endpoint for the comment carried by an event that can create/refresh
// a comment (comment.created / comment.replied). It runs in its own goroutine
// with a short timeout; any failure is logged at most and never propagates. It is
// a no-op when the event carries no comment, no server URL is configured, or the
// payload has no comment id.
func (s *Server) ackReceived(event string, body []byte) {
	if event != "comment.created" && event != "comment.replied" {
		return
	}
	if s.cfg.ServerURL == "" {
		return
	}
	id := commentID(body)
	if id == "" {
		return
	}
	url := strings.TrimRight(s.cfg.ServerURL, "/") + "/api/comments/" + id + "/received"
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
		if err != nil {
			log.Printf("runner: received-ack build failed: %v", err)
			return
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Printf("runner: received-ack POST failed: %v", err)
			return
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
}

// Handler returns the runner's HTTP handler (webhook at "/", health at
// "/healthz").
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok\n")
	})
	mux.HandleFunc("/", s.handleWebhook)
	return mux
}
