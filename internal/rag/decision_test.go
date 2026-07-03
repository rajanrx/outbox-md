package rag

import (
	"context"
	"strings"
	"testing"
)

// newIndexer wires an indexer over a fresh store with the given embedder.
func newIndexer(t *testing.T, emb Embedder) (*Indexer, *Store) {
	t.Helper()
	s := newStore(t)
	ix := NewIndexer(s, emb, nil, func(string, ...any) {})
	return ix, s
}

// TestIndexDecisionRetrievableByKindDecision: a decision written via IndexDecision
// is retrievable with SearchKind(kind="decision"), and the kind="doc" default
// search EXCLUDES it — the two corpora are separated by the kind filter (§5.4).
func TestIndexDecisionRetrievableByKindDecision(t *testing.T) {
	ix, s := newIndexer(t, &stubEmbedder{})
	searcher := NewSearcher(s, &stubEmbedder{}, ix.Mode)

	// A served doc chunk (kind=doc) about apples.
	if err := ix.Reindex(context.Background(), "p", "spec.md", "# Fruit\napple orchard notes"); err != nil {
		t.Fatal(err)
	}
	// A decision about a banana ruling (kind=decision).
	if err := ix.IndexDecision(context.Background(), "p", "spec.md", "Council verdict: edit",
		"# Council verdict: edit\n\nOutcome: the banana wording was tightened per the council."); err != nil {
		t.Fatal(err)
	}

	// Default (doc) search for "banana" must NOT surface the decision.
	docHits, err := searcher.Search("p", "banana verdict", 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range docHits {
		if h.Kind == KindDecision {
			t.Fatalf("default doc search leaked a decision: %+v", h)
		}
	}

	// kind=decision search must return the decision, tagged as such.
	decHits, err := searcher.SearchKind("p", "banana verdict", KindDecision, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(decHits) == 0 {
		t.Fatal("kind=decision search returned no hits for an indexed decision")
	}
	found := false
	for _, h := range decHits {
		if h.Kind != KindDecision {
			t.Fatalf("kind=decision search returned a non-decision hit: %+v", h)
		}
		if strings.Contains(strings.ToLower(h.Snippet), "banana") {
			found = true
		}
	}
	if !found {
		t.Fatalf("decision content not retrieved; hits=%+v", decHits)
	}
}

// TestDecisionSurvivesDocReindex: re-indexing (or deleting) the served doc that a
// decision cites must NOT drop the decision — decision memory is durable (§5.4).
func TestDecisionSurvivesDocReindex(t *testing.T) {
	ix, s := newIndexer(t, &stubEmbedder{})
	searcher := NewSearcher(s, &stubEmbedder{}, ix.Mode)

	if err := ix.IndexDecision(context.Background(), "p", "spec.md", "Council verdict: edit",
		"# Council verdict: edit\n\nOutcome: banana wording tightened."); err != nil {
		t.Fatal(err)
	}
	// Re-index the same doc path repeatedly (delete-then-insert of doc chunks).
	for i := 0; i < 2; i++ {
		if err := ix.Reindex(context.Background(), "p", "spec.md", "# Doc\nfresh doc body"); err != nil {
			t.Fatal(err)
		}
	}
	// Deleting the doc must not remove the decision either.
	if err := s.DeleteDoc("p", "spec.md"); err != nil {
		t.Fatal(err)
	}

	decHits, err := searcher.SearchKind("p", "banana", KindDecision, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(decHits) == 0 {
		t.Fatal("decision was dropped by a doc re-index/delete — memory must be durable")
	}
}

// TestIndexDecisionNonBlockingOnEmbedError: a failing embedder must NOT block a
// decision write — the record is still indexed as text (keyword-retrievable) and
// AppendChunks succeeds. Mirrors Reindex's non-blocking contract.
func TestIndexDecisionNonBlockingOnEmbedError(t *testing.T) {
	ix, s := newIndexer(t, &stubEmbedder{fail: true})

	if err := ix.IndexDecision(context.Background(), "p", "spec.md", "Council verdict: edit",
		"# Council verdict: edit\n\nOutcome: carrot wording tightened."); err != nil {
		t.Fatalf("IndexDecision returned error on embed failure (should be non-blocking): %v", err)
	}
	// The decision is still keyword-retrievable via the FTS/lexical layer.
	hits, err := s.LexicalSearchKind("p", "carrot", KindDecision, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("decision not indexed as text after embed failure")
	}
	if ix.Mode.Mode() != ModeDegraded {
		t.Fatalf("embed failure did not flip mode to degraded; got %q", ix.Mode.Mode())
	}
}
