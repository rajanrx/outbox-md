package rag

import (
	"context"
	"testing"
)

func indexDoc(t *testing.T, ix *Indexer, project, path, content string) {
	t.Helper()
	if err := ix.Reindex(context.Background(), project, path, content); err != nil {
		t.Fatal(err)
	}
}

// fruit docs: each heading section is dominated by one keyword the stub embedder
// vectorises, so cosine ranking is deterministic.
const (
	appleDoc  = "# Apple\n\napple apple apple: a crisp sweet fruit that grows on trees."
	bananaDoc = "# Banana\n\nbanana banana banana: a soft yellow tropical fruit."
	carrotDoc = "# Carrot\n\ncarrot carrot carrot: an orange root vegetable."
)

func TestSearchSemanticRanksClosestFirst(t *testing.T) {
	s := newStore(t)
	emb := &stubEmbedder{}
	mode := NewModeTracker(true, func(string, ...any) {})
	ix := NewIndexer(s, emb, mode, func(string, ...any) {})
	indexDoc(t, ix, "p", "apple.md", appleDoc)
	indexDoc(t, ix, "p", "banana.md", bananaDoc)
	indexDoc(t, ix, "p", "carrot.md", carrotDoc)

	srch := NewSearcher(s, emb, mode)
	results, err := srch.Search("p", "apple", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("no results")
	}
	if results[0].DocPath != "apple.md" {
		t.Fatalf("top result = %q, want apple.md (semantically closest)", results[0].DocPath)
	}
	// The vector path contributed → source is semantic or hybrid, not pure keyword.
	if results[0].Source == "keyword" {
		t.Fatalf("top result source = keyword, expected vector contribution")
	}
}

func TestSearchKeywordOnlyWithoutEmbedder(t *testing.T) {
	s := newStore(t)
	// No embedder: FTS5 keyword layer must still return hits (degrade, not break).
	ix := NewIndexer(s, nil, NewModeTracker(false, func(string, ...any) {}), func(string, ...any) {})
	indexDoc(t, ix, "p", "apple.md", appleDoc)
	indexDoc(t, ix, "p", "banana.md", bananaDoc)

	srch := NewSearcher(s, nil, ix.Mode)
	results, err := srch.Search("p", "banana", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("keyword-only search returned no hits")
	}
	if results[0].DocPath != "banana.md" {
		t.Fatalf("top keyword result = %q, want banana.md", results[0].DocPath)
	}
	if results[0].Source != "keyword" {
		t.Fatalf("source = %q, want keyword (no embedder)", results[0].Source)
	}
	if ix.Mode.Mode() != ModeKeywordOnly {
		t.Fatalf("mode = %q, want keyword-only", ix.Mode.Mode())
	}
}

func TestSearchScopedToProject(t *testing.T) {
	s := newStore(t)
	emb := &stubEmbedder{}
	mode := NewModeTracker(true, func(string, ...any) {})
	ix := NewIndexer(s, emb, mode, func(string, ...any) {})
	indexDoc(t, ix, "p1", "a.md", appleDoc)
	indexDoc(t, ix, "p2", "b.md", appleDoc) // same content, different project

	srch := NewSearcher(s, emb, mode)
	results, err := srch.Search("p1", "apple", 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.DocPath != "a.md" {
			t.Fatalf("cross-project leak: got %q, want only a.md", r.DocPath)
		}
	}
	if len(results) == 0 {
		t.Fatal("no results in scoped project")
	}
}
