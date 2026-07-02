package rag

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

// CmdEmbedder is the shell-out Embedder (design §5.2). For each text it runs the
// configured command, writes the text to the command's stdin, and reads a JSON
// array of float64 from its stdout — the vector for that one text. Batching is a
// loop: Embed runs the command once per input text (per-text protocol, chosen
// over array-of-arrays for a trivially simple wrapper contract). The command is
// exec'd directly (no shell); argv is split on whitespace like agent_cmd, so a
// wrapper with plain arguments works and there is no shell-injection surface.
//
// ollama has no stdin→vector CLI, so `ollama run embeddinggemma` (which emits
// prose) will NOT work directly — embed_cmd expects a wrapper script that reads
// stdin and prints a JSON float array (e.g. a curl to the ollama /api/embeddings
// endpoint piped through jq). With no embed_cmd configured the KB runs FTS5-only.
type CmdEmbedder struct {
	cmd string // the raw command template, argv split on whitespace

	mu   sync.Mutex
	dims int // discovered from the first successful embed; 0 until then
}

// NewCmdEmbedder builds a shell-out embedder for the given command template.
func NewCmdEmbedder(cmd string) *CmdEmbedder {
	return &CmdEmbedder{cmd: strings.TrimSpace(cmd)}
}

// ID is "cmd:" + the first 8 hex chars of sha256(command). It changes whenever
// the command string changes, so re-pointing embed_cmd invalidates the old
// vectors and forces a re-index (the ID()+Dims() index key, design §5.2).
func (e *CmdEmbedder) ID() string {
	sum := sha256.Sum256([]byte(e.cmd))
	return "cmd:" + hex.EncodeToString(sum[:])[:8]
}

// Dims returns the dimensionality discovered from the first successful embed, or
// 0 before any text has been embedded.
func (e *CmdEmbedder) Dims() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.dims
}

// Embed runs the command once per text and returns the parsed vectors in order.
// A nil/empty input returns nil with no command run. Any command or parse failure
// returns an error and no partial results (the caller keeps text/FTS5 indexed and
// marks the chunks embedding-stale for retry — the embedder itself just reports).
func (e *CmdEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	out := make([][]float32, 0, len(texts))
	for i, text := range texts {
		vec, err := e.embedOne(ctx, text)
		if err != nil {
			return nil, fmt.Errorf("embed_cmd: text %d/%d: %w", i+1, len(texts), err)
		}
		out = append(out, vec)
	}
	return out, nil
}

func (e *CmdEmbedder) embedOne(ctx context.Context, text string) ([]float32, error) {
	args := splitArgs(e.cmd)
	if len(args) == 0 {
		return nil, fmt.Errorf("empty command")
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stdin = strings.NewReader(text)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return nil, fmt.Errorf("%w: %s", err, msg)
		}
		return nil, err
	}
	var raw []float64
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &raw); err != nil {
		return nil, fmt.Errorf("parse vector: %w", err)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("empty vector")
	}
	vec := make([]float32, len(raw))
	for i, f := range raw {
		vec[i] = float32(f)
	}
	e.recordDims(len(vec))
	return vec, nil
}

// recordDims caches the dimensionality from the first successful embed.
func (e *CmdEmbedder) recordDims(n int) {
	e.mu.Lock()
	if e.dims == 0 {
		e.dims = n
	}
	e.mu.Unlock()
}

// splitArgs splits a command template into argv on whitespace, mirroring the
// autoreply spawn contract (strings.Fields, exec, no shell). Quoted paths with
// spaces are not supported — deliberate parity with agent_cmd for this slice.
func splitArgs(cmd string) []string { return strings.Fields(cmd) }

// trimSpace is a tiny local helper so callers don't import strings just for this.
func trimSpace(s string) string { return strings.TrimSpace(s) }
