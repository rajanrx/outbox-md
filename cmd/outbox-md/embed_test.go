package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestEmbedRunStdinToVector drives `embed run` against a stub ollama and asserts
// the exact CmdEmbedder contract: a text on stdin → a JSON float array on stdout,
// and nothing else (a stray byte would corrupt the parsed vector).
func TestEmbedRunStdinToVector(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embeddings" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		var body struct {
			Model  string `json:"model"`
			Prompt string `json:"prompt"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Prompt != "hello world" {
			t.Errorf("prompt = %q, want %q", body.Prompt, "hello world")
		}
		_, _ = w.Write([]byte(`{"embedding":[0.1,0.2,0.3]}`))
	}))
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	err := embedRun([]string{"--url", srv.URL, "--model", "embeddinggemma"},
		strings.NewReader("hello world"), &stdout, &stderr)
	if err != nil {
		t.Fatalf("embed run: %v (stderr=%s)", err, stderr.String())
	}
	// stdout must be EXACTLY a JSON float array — parse it back as CmdEmbedder does.
	var vec []float64
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &vec); err != nil {
		t.Fatalf("stdout is not a JSON float array: %q (%v)", stdout.String(), err)
	}
	if len(vec) != 3 || vec[0] != 0.1 || vec[1] != 0.2 || vec[2] != 0.3 {
		t.Fatalf("vector = %v, want [0.1 0.2 0.3]", vec)
	}
}

// TestEmbedRunFallsBackToEmbedEndpoint verifies that when /api/embeddings is gone
// (404 on newer ollama), embed run retries /api/embed and parses embeddings[][].
func TestEmbedRunFallsBackToEmbedEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/embeddings":
			http.NotFound(w, r)
		case "/api/embed":
			_, _ = w.Write([]byte(`{"embeddings":[[1.5,2.5]]}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	if err := embedRun([]string{"--url", srv.URL}, strings.NewReader("x"), &stdout, &stderr); err != nil {
		t.Fatalf("embed run: %v (stderr=%s)", err, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != "[1.5,2.5]" {
		t.Fatalf("stdout = %q, want [1.5,2.5]", got)
	}
}

// TestEmbedRunErrorsOnHTTP500 asserts a server error exits non-zero (returns an
// error) with a descriptive message and writes NOTHING to stdout.
func TestEmbedRunErrorsOnHTTP500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	err := embedRun([]string{"--url", srv.URL}, strings.NewReader("x"), &stdout, &stderr)
	if err == nil {
		t.Fatal("expected an error on HTTP 500")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("error %q should mention the HTTP status", err.Error())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout must be empty on failure, got %q", stdout.String())
	}
}

// TestEmbedRunErrorsWhenUnreachable asserts an unreachable URL is a non-zero exit
// with a clear message (nothing listening → transport error).
func TestEmbedRunErrorsWhenUnreachable(t *testing.T) {
	var stdout, stderr bytes.Buffer
	// 127.0.0.1:1 has nothing listening.
	err := embedRun([]string{"--url", "http://127.0.0.1:1"}, strings.NewReader("x"), &stdout, &stderr)
	if err == nil {
		t.Fatal("expected an error when ollama is unreachable")
	}
	if !strings.Contains(err.Error(), "unreachable") {
		t.Fatalf("error %q should say ollama is unreachable", err.Error())
	}
}
