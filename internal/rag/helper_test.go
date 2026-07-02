package rag

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
)

// TestMain doubles the test binary as a fake embedder command: when
// RAG_FAKE_EMBED is set in the environment, the process reads a text on stdin and
// prints a JSON float array on stdout (the embed_cmd contract), then exits. This
// is the standard Go helper-process idiom — a real external command exercised
// through the actual exec path, no shell script on disk.
func TestMain(m *testing.M) {
	if os.Getenv("RAG_FAKE_EMBED") != "" {
		fakeEmbedProcess()
		return
	}
	os.Exit(m.Run())
}

// fakeEmbedProcess is the child-side behaviour: consume stdin, emit a fixed
// 4-dim vector as JSON on stdout.
func fakeEmbedProcess() {
	_, _ = io.Copy(io.Discard, os.Stdin)
	fmt.Fprint(os.Stdout, "[0.1, 0.2, 0.3, 0.4]")
	os.Exit(0)
}

// fakeEmbedCmd returns a command string that invokes THIS test binary as the fake
// embedder (via the RAG_FAKE_EMBED env gate set by the test). exec inherits the
// parent env, so the child sees the gate and runs fakeEmbedProcess.
func fakeEmbedCmd(t *testing.T) string {
	t.Helper()
	t.Setenv("RAG_FAKE_EMBED", "1")
	return os.Args[0]
}

// stubEmbedder is a deterministic in-process embedder for index/search tests. It
// maps text to a fixed-dim vector by counting keyword occurrences, so cosine
// similarity is predictable: a query about "apple" is closest to the apple chunk.
type stubEmbedder struct {
	id   string
	fail bool // when true, Embed returns an error (degraded-path tests)
}

var stubKeywords = []string{"apple", "banana", "carrot"}

func (s *stubEmbedder) ID() string {
	if s.id != "" {
		return s.id
	}
	return "stub:v1"
}

func (s *stubEmbedder) Dims() int { return len(stubKeywords) }

func (s *stubEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	if s.fail {
		return nil, fmt.Errorf("stub embedder: forced failure")
	}
	out := make([][]float32, len(texts))
	for i, text := range texts {
		lower := strings.ToLower(text)
		vec := make([]float32, len(stubKeywords))
		for j, kw := range stubKeywords {
			vec[j] = float32(strings.Count(lower, kw))
		}
		out[i] = vec
	}
	return out, nil
}
