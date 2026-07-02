package rag

import (
	"context"
	"testing"
)

func TestReindexWithoutEmbedderIndexesTextOnly(t *testing.T) {
	s := newStore(t)
	ix := NewIndexer(s, nil, nil, func(string, ...any) {})
	if err := ix.Reindex(context.Background(), "p", "d.md", "# H\n\napple pie recipe"); err != nil {
		t.Fatal(err)
	}
	// Text/FTS present.
	if got := countChunks(t, s, "p", "d.md"); got == 0 {
		t.Fatal("no chunks indexed")
	}
	if hits, _ := s.LexicalSearch("p", "apple", 5); len(hits) == 0 {
		t.Fatal("FTS did not index text-only chunk")
	}
	// No vectors (no embedder).
	if got, _ := s.VectorChunks("p", "any", 3); len(got) != 0 {
		t.Fatalf("vector chunks = %d without embedder, want 0", len(got))
	}
	if ix.Mode.Mode() != ModeKeywordOnly {
		t.Fatalf("mode = %q, want keyword-only", ix.Mode.Mode())
	}
}

func TestReindexEmbedFailureIsNonBlocking(t *testing.T) {
	s := newStore(t)
	emb := &stubEmbedder{fail: true}
	mode := NewModeTracker(true, func(string, ...any) {})
	ix := NewIndexer(s, emb, mode, func(string, ...any) {})

	// A failing embedder must NOT block: Reindex returns nil, text/FTS indexed,
	// mode flips to degraded, chunks are embedding-stale (retriable).
	if err := ix.Reindex(context.Background(), "p", "d.md", "# H\n\nbanana bread"); err != nil {
		t.Fatalf("embed failure blocked reindex: %v", err)
	}
	if got := countChunks(t, s, "p", "d.md"); got == 0 {
		t.Fatal("embed failure lost the text index")
	}
	if hits, _ := s.LexicalSearch("p", "banana", 5); len(hits) == 0 {
		t.Fatal("FTS should still serve after embed failure")
	}
	if got, _ := s.VectorChunks("p", emb.ID(), emb.Dims()); len(got) != 0 {
		t.Fatalf("stale chunks should have no vectors, got %d", len(got))
	}
	if mode.Mode() != ModeDegraded {
		t.Fatalf("mode = %q, want degraded", mode.Mode())
	}
}

func TestReindexReplacesOnReindex(t *testing.T) {
	s := newStore(t)
	ix := NewIndexer(s, &stubEmbedder{}, nil, func(string, ...any) {})
	indexDoc(t, ix, "p", "d.md", appleDoc)
	first := countChunks(t, s, "p", "d.md")
	if first == 0 {
		t.Fatal("no chunks after first index")
	}
	// Re-index with different content: old chunks replaced, not accumulated.
	indexDoc(t, ix, "p", "d.md", "# New\n\ncarrot only now")
	if hits, _ := s.LexicalSearch("p", "apple", 5); len(hits) != 0 {
		t.Fatal("stale 'apple' content survived re-index")
	}
	if hits, _ := s.LexicalSearch("p", "carrot", 5); len(hits) == 0 {
		t.Fatal("new content not indexed")
	}
}

func TestBuildAllSettlesModeFromAggregate(t *testing.T) {
	s := newStore(t)
	emb := &stubEmbedder{}
	mode := NewModeTracker(true, func(string, ...any) {})
	ix := NewIndexer(s, emb, mode, func(string, ...any) {})
	docs := []Doc{
		{Project: "p", Path: "a.md", Content: appleDoc},
		{Project: "p", Path: "b.md", Content: bananaDoc},
	}
	if err := ix.BuildAll(context.Background(), docs); err != nil {
		t.Fatal(err)
	}
	if mode.Mode() != ModeSemantic {
		t.Fatalf("all-success BuildAll mode = %q, want semantic", mode.Mode())
	}
	// Both docs vector-indexed.
	got, _ := s.VectorChunks("p", emb.ID(), emb.Dims())
	if len(got) < 2 {
		t.Fatalf("vector chunks after BuildAll = %d, want >=2", len(got))
	}
}

func TestBuildAllDegradedWhenEmbedFails(t *testing.T) {
	s := newStore(t)
	mode := NewModeTracker(true, func(string, ...any) {})
	ix := NewIndexer(s, &stubEmbedder{fail: true}, mode, func(string, ...any) {})
	err := ix.BuildAll(context.Background(), []Doc{{Project: "p", Path: "a.md", Content: appleDoc}})
	if err != nil {
		t.Fatalf("BuildAll should not error on embed failure: %v", err)
	}
	if mode.Mode() != ModeDegraded {
		t.Fatalf("mode = %q, want degraded", mode.Mode())
	}
	// Text still indexed despite embed failure.
	if hits, _ := s.LexicalSearch("p", "apple", 5); len(hits) == 0 {
		t.Fatal("BuildAll lost text index on embed failure")
	}
}
