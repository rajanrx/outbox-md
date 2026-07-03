package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rajanrx/outbox-md/internal/config"
)

// APIEmbedder is the hosted-embedding Embedder (design §5.2): it POSTs text to an
// OpenAI-compatible /embeddings endpoint and reads back one vector per input. The
// API key is referenced by ENV-VAR NAME only (apiKeyEnv) and read fresh from the
// environment at each call — it is NEVER stored on the struct or in config/DB. A
// missing key or an API error degrades retrieval (mode → degraded, FTS5 serves)
// rather than hard-failing: only doc/decision text is ever sent to the provider.
//
// The provider switch resolves the base URL; today "openai" (and any
// OpenAI-compatible endpoint) is supported cleanly, and more providers drop in by
// extending providerBaseURL + (if their wire format differs) the request/parse
// path. Batching is one request for all texts (OpenAI accepts an input array).
type APIEmbedder struct {
	provider  string
	model     string
	apiKeyEnv string // the ENV VAR NAME to read the key from — never the key itself
	baseURL   string
	http      *http.Client

	mu   sync.Mutex
	dims int // discovered from the first successful embed; 0 until then
}

// NewAPIEmbedder builds a hosted embedder from the config block. It returns
// (nil, nil) — a supported degrade, never an error — when the referenced key env
// is empty, so a configured-but-unkeyed embed_api falls back to FTS5 without
// failing startup. The key is read here ONLY to decide live-vs-degraded; it is not
// retained. An unknown provider constructs with no base URL, so Embed fails and
// retrieval degrades rather than the server refusing to start.
func NewAPIEmbedder(cfg config.EmbedAPIConfig) (*APIEmbedder, error) {
	if strings.TrimSpace(os.Getenv(cfg.APIKeyEnv)) == "" {
		return nil, nil
	}
	return newAPIEmbedder(cfg.Provider, cfg.Model, cfg.APIKeyEnv, providerBaseURL(cfg.Provider), nil), nil
}

// newAPIEmbedder is the internal constructor (also the test seam): it takes an
// explicit base URL + http client so tests can point at an httptest server without
// any config surface for the URL.
func newAPIEmbedder(provider, model, apiKeyEnv, baseURL string, hc *http.Client) *APIEmbedder {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &APIEmbedder{
		provider:  strings.TrimSpace(provider),
		model:     strings.TrimSpace(model),
		apiKeyEnv: strings.TrimSpace(apiKeyEnv),
		baseURL:   strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		http:      hc,
	}
}

// providerBaseURL maps a provider name to its OpenAI-compatible embeddings base
// URL. "openai" (and the generic "openai-compatible") resolve to the OpenAI API;
// an unknown provider resolves to "" so Embed fails and retrieval degrades.
func providerBaseURL(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "", "openai", "openai-compatible":
		return "https://api.openai.com/v1"
	default:
		return ""
	}
}

// ID is "api:<provider>:<model>". It ENCODES provider + model, so switching either
// changes the id and — via the ID()+Dims() index key (§5.2) — forces a re-index.
func (e *APIEmbedder) ID() string {
	return fmt.Sprintf("api:%s:%s", strings.ToLower(e.provider), e.model)
}

// Dims returns the dimensionality discovered from the first successful embed, or
// 0 before any text has been embedded.
func (e *APIEmbedder) Dims() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.dims
}

// Embed sends all texts in one /embeddings request and returns the vectors in
// input order. The key is read from the environment HERE (never stored). A nil/
// empty input returns nil with no request. Any transport, status, or parse failure
// returns an error and no partial vectors — the caller keeps text/FTS5 indexed and
// flips the mode to degraded (embed_api never hard-fails the pipeline).
func (e *APIEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if e.baseURL == "" {
		return nil, fmt.Errorf("embed_api: unsupported provider %q (no endpoint)", e.provider)
	}
	key := strings.TrimSpace(os.Getenv(e.apiKeyEnv))
	if key == "" {
		return nil, fmt.Errorf("embed_api: key env %q is empty", e.apiKeyEnv)
	}

	body, err := json.Marshal(embeddingsRequest{Model: e.model, Input: texts})
	if err != nil {
		return nil, fmt.Errorf("embed_api: marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("embed_api: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)

	resp, err := e.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed_api: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("embed_api: status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var parsed embeddingsResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("embed_api: parse response: %w", err)
	}
	if len(parsed.Data) != len(texts) {
		return nil, fmt.Errorf("embed_api: got %d embeddings for %d inputs", len(parsed.Data), len(texts))
	}
	// The API contract returns embeddings in input order, but each item carries an
	// explicit index — sort by it so we never mis-align if the server reorders.
	sort.Slice(parsed.Data, func(i, j int) bool { return parsed.Data[i].Index < parsed.Data[j].Index })

	out := make([][]float32, len(parsed.Data))
	for i, d := range parsed.Data {
		if len(d.Embedding) == 0 {
			return nil, fmt.Errorf("embed_api: empty embedding at index %d", i)
		}
		vec := make([]float32, len(d.Embedding))
		for j, f := range d.Embedding {
			vec[j] = float32(f)
		}
		out[i] = vec
	}
	e.recordDims(len(out[0]))
	return out, nil
}

// recordDims caches the dimensionality from the first successful embed.
func (e *APIEmbedder) recordDims(n int) {
	e.mu.Lock()
	if e.dims == 0 {
		e.dims = n
	}
	e.mu.Unlock()
}

type embeddingsRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embeddingsResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float64 `json:"embedding"`
	} `json:"data"`
}
