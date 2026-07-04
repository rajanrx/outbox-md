package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// defaultOllamaURL is the local ollama daemon endpoint the embed bridge talks to.
const defaultOllamaURL = "http://localhost:11434"

// defaultEmbedModel is the embedding model outbox pulls + points embed_cmd at. It
// is a small, fast, local model that runs comfortably on a laptop.
const defaultEmbedModel = "embeddinggemma"

// embedHTTPTimeout bounds a single ollama embeddings call. It is deliberately
// generous: the FIRST embed after a cold start loads the model into memory, which
// can take well over a minute, and the KB reconcile passes a context with no
// deadline (context.Background), so this client timeout is the only bound.
const embedHTTPTimeout = 180 * time.Second

// embedRun is the embedder bridge: it reads ONE text from stdin (all of it), asks
// the local ollama daemon to embed it, and writes the resulting JSON float array
// to stdout — exactly the CmdEmbedder stdin→stdout contract (internal/rag). Because
// outbox is its own embedder, embed_cmd needs no curl/jq wrapper. Any failure
// (ollama down, model missing, HTTP error, empty vector) returns a descriptive
// error; main() prints it to stderr and exits non-zero, which CmdEmbedder surfaces.
// ONLY the JSON array is ever written to stdout — a stray stdout byte would corrupt
// the parsed vector.
func embedRun(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("embed run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { usageFor(stderr, "embed") }
	model := fs.String("model", defaultEmbedModel, "ollama embedding model")
	url := fs.String("url", defaultOllamaURL, "ollama base URL")
	if err := fs.Parse(args); err != nil {
		return err
	}

	text, err := io.ReadAll(stdin)
	if err != nil {
		return fmt.Errorf("embed run: read stdin: %w", err)
	}

	vec, err := ollamaEmbed(context.Background(), &http.Client{Timeout: embedHTTPTimeout}, *url, *model, string(text))
	if err != nil {
		return err
	}

	enc, err := json.Marshal(vec)
	if err != nil {
		return fmt.Errorf("embed run: marshal vector: %w", err)
	}
	_, err = stdout.Write(enc)
	return err
}

// ollamaEmbed POSTs text to ollama and returns the embedding as a []float64. It
// prefers the classic /api/embeddings endpoint ({"model","prompt"} →
// {"embedding":[…]}); when that endpoint is gone (404/405 on newer ollama), it
// falls back to /api/embed ({"model","input"} → {"embeddings":[[…]]}). A transport
// error, non-2xx status, or empty vector is an error (never a partial result).
func ollamaEmbed(ctx context.Context, hc *http.Client, baseURL, model, text string) ([]float64, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		base = defaultOllamaURL
	}

	// Primary: /api/embeddings (prompt → embedding).
	vec, status, err := postEmbeddings(ctx, hc, base, model, text)
	if err == nil {
		return vec, nil
	}
	// Fall back to /api/embed only when the primary endpoint is absent (deprecated
	// on newer ollama); a real embed error (model missing, daemon down) is returned.
	if status == http.StatusNotFound || status == http.StatusMethodNotAllowed {
		return postEmbed(ctx, hc, base, model, text)
	}
	return nil, err
}

// postEmbeddings calls the classic /api/embeddings endpoint. It returns the HTTP
// status alongside the error so the caller can decide whether to fall back.
func postEmbeddings(ctx context.Context, hc *http.Client, base, model, text string) ([]float64, int, error) {
	body, _ := json.Marshal(map[string]string{"model": model, "prompt": text})
	raw, status, err := doOllamaPost(ctx, hc, base+"/api/embeddings", body)
	if err != nil {
		return nil, status, err
	}
	var parsed struct {
		Embedding []float64 `json:"embedding"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, status, fmt.Errorf("embed run: parse ollama response: %w", err)
	}
	if len(parsed.Embedding) == 0 {
		return nil, status, fmt.Errorf("embed run: ollama returned an empty embedding (is model %q pulled? run 'outbox embed setup')", model)
	}
	return parsed.Embedding, status, nil
}

// postEmbed calls the newer /api/embed endpoint (input → embeddings[][]).
func postEmbed(ctx context.Context, hc *http.Client, base, model, text string) ([]float64, error) {
	body, _ := json.Marshal(map[string]any{"model": model, "input": text})
	raw, _, err := doOllamaPost(ctx, hc, base+"/api/embed", body)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Embeddings [][]float64 `json:"embeddings"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("embed run: parse ollama response: %w", err)
	}
	if len(parsed.Embeddings) == 0 || len(parsed.Embeddings[0]) == 0 {
		return nil, fmt.Errorf("embed run: ollama returned an empty embedding (is model %q pulled? run 'outbox embed setup')", model)
	}
	return parsed.Embeddings[0], nil
}

// doOllamaPost POSTs body to url and returns the response bytes + status. A non-2xx
// status is an error carrying the status and (trimmed) body; the status is returned
// even on error so the caller can branch on 404/405 for the endpoint fallback.
func doOllamaPost(ctx context.Context, hc *http.Client, url string, body []byte) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, 0, fmt.Errorf("embed run: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("embed run: ollama unreachable at %s — is it running? (start it, or run 'outbox embed setup'): %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp.StatusCode, fmt.Errorf("embed run: ollama HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return raw, resp.StatusCode, nil
}
