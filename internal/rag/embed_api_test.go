package rag

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rajanrx/outbox-md/internal/config"
)

// embeddingsStub is an httptest handler mimicking an OpenAI-compatible
// /embeddings endpoint: it echoes one 3-dim vector per input, tagged with its
// index (deliberately returned in REVERSED order to prove index-sorting).
func embeddingsStub(t *testing.T, wantKey string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+wantKey {
			t.Errorf("auth header = %q, want Bearer %s", got, wantKey)
		}
		if !strings.HasSuffix(r.URL.Path, "/embeddings") {
			t.Errorf("path = %q, want .../embeddings", r.URL.Path)
		}
		var req embeddingsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		type item struct {
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		}
		items := make([]item, len(req.Input))
		for i := range req.Input {
			items[i] = item{Index: i, Embedding: []float64{float64(i), 0.5, 0.25}}
		}
		// Reverse the order so a naive reader would mis-align.
		for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
			items[i], items[j] = items[j], items[i]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": items})
	}))
}

// TestAPIEmbedderReturnsVectorsAndDiscoversDims: the api embedder POSTs to the
// stub, returns vectors in INPUT order (index-sorted despite a reversed response),
// and discovers Dims from the first embed.
func TestAPIEmbedderReturnsVectorsAndDiscoversDims(t *testing.T) {
	t.Setenv("EMBED_KEY", "sk-test-123")
	srv := embeddingsStub(t, "sk-test-123")
	defer srv.Close()

	emb := newAPIEmbedder("openai", "text-embedding-3-small", "EMBED_KEY", srv.URL, srv.Client())
	if emb.Dims() != 0 {
		t.Fatalf("Dims before any embed = %d, want 0", emb.Dims())
	}
	vecs, err := emb.Embed(context.Background(), []string{"first", "second", "third"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 3 {
		t.Fatalf("got %d vectors, want 3", len(vecs))
	}
	// Input order preserved: vector i carries i as its leading component.
	for i, v := range vecs {
		if len(v) != 3 {
			t.Fatalf("vector %d has %d dims, want 3", i, len(v))
		}
		if v[0] != float32(i) {
			t.Fatalf("vector %d mis-aligned: leading = %v, want %d", i, v[0], i)
		}
	}
	if emb.Dims() != 3 {
		t.Fatalf("Dims after embed = %d, want 3", emb.Dims())
	}
}

// TestAPIEmbedderIDEncodesProviderAndModel: switching provider or model changes
// ID() so the index key (§5.2) invalidates and re-indexes.
func TestAPIEmbedderIDEncodesProviderAndModel(t *testing.T) {
	a := newAPIEmbedder("openai", "text-embedding-3-small", "EMBED_KEY", "http://x", nil)
	if !strings.Contains(a.ID(), "openai") || !strings.Contains(a.ID(), "text-embedding-3-small") {
		t.Fatalf("ID %q does not encode provider+model", a.ID())
	}
	b := newAPIEmbedder("openai", "text-embedding-3-large", "EMBED_KEY", "http://x", nil)
	if a.ID() == b.ID() {
		t.Fatalf("different models share an ID (%q) — a switch would not re-index", a.ID())
	}
	c := newAPIEmbedder("cohere", "text-embedding-3-small", "EMBED_KEY", "http://x", nil)
	if a.ID() == c.ID() {
		t.Fatalf("different providers share an ID (%q)", a.ID())
	}
}

// TestNewAPIEmbedderEmptyKeyDegrades: an empty key env constructs to a nil
// embedder (degrade to FTS5), never a panic or a hard error.
func TestNewAPIEmbedderEmptyKeyDegrades(t *testing.T) {
	t.Setenv("EMPTY_KEY", "")
	emb, err := NewAPIEmbedder(config.EmbedAPIConfig{Provider: "openai", Model: "m", APIKeyEnv: "EMPTY_KEY"})
	if err != nil {
		t.Fatalf("empty key returned an error: %v", err)
	}
	if emb != nil {
		t.Fatal("empty key did not degrade to a nil embedder")
	}
}

// TestAPIEmbedderErrorIsNotFatal: a 5xx from the endpoint returns an error (which
// the caller turns into a degraded mode), not a panic — never a hard failure.
func TestAPIEmbedderErrorIsNotFatal(t *testing.T) {
	t.Setenv("EMBED_KEY", "sk-test-123")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "upstream boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	emb := newAPIEmbedder("openai", "m", "EMBED_KEY", srv.URL, srv.Client())
	if _, err := emb.Embed(context.Background(), []string{"x"}); err == nil {
		t.Fatal("expected an error from a 500 response")
	}
	// A degraded searcher over a failing embedder still serves FTS5 (no panic).
	s := newStore(t)
	if err := s.UpsertDoc("p", "d.md", []ChunkRow{{Project: "p", DocPath: "d.md", Text: "apple pie"}}); err != nil {
		t.Fatal(err)
	}
	searcher := NewSearcher(s, emb, nil)
	hits, err := searcher.Search("p", "apple", 5)
	if err != nil {
		t.Fatalf("search over a failing embedder errored instead of degrading: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("degraded search returned no FTS hits")
	}
	if searcher.Mode.Mode() != ModeDegraded {
		t.Fatalf("mode = %q, want degraded after an embed failure", searcher.Mode.Mode())
	}
}

// TestNewEmbedderPrefersEmbedAPIOverEmbedCmd: when embed_api is configured (and
// keyed), the factory returns the API embedder even if embed_cmd is also set —
// embed_api overrides embed_cmd (§5.2).
func TestNewEmbedderPrefersEmbedAPIOverEmbedCmd(t *testing.T) {
	t.Setenv("EMBED_KEY", "sk-test-123")
	cfg := config.Config{
		EmbedCmd: "some-cmd",
		EmbedAPI: config.EmbedAPIConfig{Provider: "openai", Model: "text-embedding-3-small", APIKeyEnv: "EMBED_KEY"},
	}
	emb, err := NewEmbedder(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if emb == nil {
		t.Fatal("factory returned no embedder despite a keyed embed_api")
	}
	if !strings.HasPrefix(emb.ID(), "api:") {
		t.Fatalf("factory chose %q, want the api embedder (embed_api overrides embed_cmd)", emb.ID())
	}
}

// TestNewEmbedderEmbedAPIUnkeyedFallsToFTSNotCmd: embed_api configured but unkeyed
// degrades to FTS5 (nil embedder) and does NOT fall back to embed_cmd — embed_api
// set ⇒ it wins the selection.
func TestNewEmbedderEmbedAPIUnkeyedFallsToFTSNotCmd(t *testing.T) {
	t.Setenv("EMBED_KEY", "")
	cfg := config.Config{
		EmbedCmd: "some-cmd",
		EmbedAPI: config.EmbedAPIConfig{Provider: "openai", Model: "m", APIKeyEnv: "EMBED_KEY"},
	}
	emb, err := NewEmbedder(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if emb != nil {
		t.Fatalf("unkeyed embed_api fell back to %q instead of degrading to FTS5", emb.ID())
	}
}
