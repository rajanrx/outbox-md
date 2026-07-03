package rag

import (
	"context"
	"testing"
)

// TestReconcileReembedsOfflineEdit: a doc whose content changed while the server
// was down (its stored hash no longer matches the on-disk content) is re-embedded
// at startup, and its stale content is gone.
func TestReconcileReembedsOfflineEdit(t *testing.T) {
	s := newStore(t)
	ix := NewIndexer(s, &stubEmbedder{}, nil, func(string, ...any) {})
	// Initial index: apple content.
	indexDoc(t, ix, "p", "d.md", appleDoc)

	// Reconcile against NEW on-disk content (an offline edit): carrot now.
	stats, err := ix.Reconcile(context.Background(), []Doc{
		{Project: "p", Path: "d.md", Content: carrotDoc},
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Reindexed != 1 {
		t.Fatalf("reindexed = %d, want 1 (the edited doc)", stats.Reindexed)
	}
	if hits, _ := s.LexicalSearch("p", "apple", 5); len(hits) != 0 {
		t.Fatal("stale 'apple' content survived reconcile")
	}
	if hits, _ := s.LexicalSearch("p", "carrot", 5); len(hits) == 0 {
		t.Fatal("edited 'carrot' content not re-indexed")
	}
}

// TestReconcileDropsRemovedDoc: a doc that was indexed but is no longer in the
// served set (deleted/renamed while offline) has its chunks dropped at startup.
func TestReconcileDropsRemovedDoc(t *testing.T) {
	s := newStore(t)
	ix := NewIndexer(s, &stubEmbedder{}, nil, func(string, ...any) {})
	indexDoc(t, ix, "p", "keep.md", appleDoc)
	indexDoc(t, ix, "p", "gone.md", bananaDoc)

	// Reconcile with ONLY keep.md served — gone.md must be dropped.
	stats, err := ix.Reconcile(context.Background(), []Doc{
		{Project: "p", Path: "keep.md", Content: appleDoc},
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Dropped != 1 {
		t.Fatalf("dropped = %d, want 1 (gone.md)", stats.Dropped)
	}
	if got := countChunks(t, s, "p", "gone.md"); got != 0 {
		t.Fatalf("gone.md still has %d chunks after reconcile", got)
	}
	if got := countChunks(t, s, "p", "keep.md"); got == 0 {
		t.Fatal("keep.md was wrongly dropped")
	}
	// keep.md unchanged (same content) is not re-embedded.
	if stats.Unchanged != 1 {
		t.Fatalf("unchanged = %d, want 1 (keep.md)", stats.Unchanged)
	}
}

// TestReconcileFreshDbIndexesEverything: on an empty index every served doc is
// missing, so reconcile is a full build (all re-embedded, none dropped).
func TestReconcileFreshDbIndexesEverything(t *testing.T) {
	s := newStore(t)
	ix := NewIndexer(s, &stubEmbedder{}, nil, func(string, ...any) {})
	stats, err := ix.Reconcile(context.Background(), []Doc{
		{Project: "p", Path: "a.md", Content: appleDoc},
		{Project: "p", Path: "b.md", Content: bananaDoc},
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Reindexed != 2 || stats.Unchanged != 0 || stats.Dropped != 0 {
		t.Fatalf("fresh reconcile stats = %+v, want {Reindexed:2}", stats)
	}
}

// TestReconcileReembedsWhenEmbedderAppears pins the install-then-restart upgrade:
// docs indexed keyword-only (no embedder) are re-embedded on the next start once
// an embedder is configured, even though their content hash is unchanged.
func TestReconcileReembedsWhenEmbedderAppears(t *testing.T) {
	s := newStore(t)
	// First run: no embedder — keyword-only chunks, no vectors.
	kw := NewIndexer(s, nil, nil, func(string, ...any) {})
	indexDoc(t, kw, "p", "d.md", appleDoc)

	emb := &stubEmbedder{}
	if got, _ := s.VectorChunks("p", emb.ID(), emb.Dims()); len(got) != 0 {
		t.Fatal("precondition: keyword-only index should carry no vectors")
	}

	// Second run: embedder now configured. Same content, but no vectors for it yet.
	ix := NewIndexer(s, emb, NewModeTracker(true, func(string, ...any) {}), func(string, ...any) {})
	stats, err := ix.Reconcile(context.Background(), []Doc{
		{Project: "p", Path: "d.md", Content: appleDoc},
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Reindexed != 1 {
		t.Fatalf("reindexed = %d, want 1 (embedder appeared)", stats.Reindexed)
	}
	if got, _ := s.VectorChunks("p", emb.ID(), emb.Dims()); len(got) == 0 {
		t.Fatal("doc not vector-embedded after embedder appeared")
	}
	if ix.Mode.Mode() != ModeSemantic {
		t.Fatalf("mode = %q, want semantic after successful re-embed", ix.Mode.Mode())
	}
}
