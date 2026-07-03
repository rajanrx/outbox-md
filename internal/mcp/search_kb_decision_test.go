package mcp

import (
	"context"
	"testing"

	"github.com/rajanrx/outbox-md/internal/config"
	"github.com/rajanrx/outbox-md/internal/rag"
	"github.com/rajanrx/outbox-md/internal/service"
	"github.com/rajanrx/outbox-md/internal/store"
)

// seedKBWithDecision builds a rag store, indexes the given docs (kind=doc) and one
// decision (kind=decision, keyed to the given doc path), and returns the searcher.
func seedKBWithDecision(t *testing.T, st *store.Store, docs []rag.Doc, decProject, decDocPath, decText string) *rag.Searcher {
	t.Helper()
	rs, err := rag.NewStore(st.DB)
	if err != nil {
		t.Fatal(err)
	}
	ix := rag.NewIndexer(rs, nil, nil, func(string, ...any) {})
	for _, d := range docs {
		if err := ix.Reindex(context.Background(), d.Project, d.Path, d.Content); err != nil {
			t.Fatal(err)
		}
	}
	if err := ix.IndexDecision(context.Background(), decProject, decDocPath, "Council verdict: edit", decText); err != nil {
		t.Fatal(err)
	}
	return rag.NewSearcher(rs, nil, ix.Mode)
}

// TestSearchKnowledgeBaseKindFiltersDecisions: the MCP tool's kind param routes
// retrieval — the default ("doc") excludes decisions, kind="decision" restricts to
// them (§5.4). Every returned hit carries its kind.
func TestSearchKnowledgeBaseKindFiltersDecisions(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _, _ string) error { return nil })
	searcher := seedKBWithDecision(t, s,
		[]rag.Doc{{Project: "p", Path: "spec.md", Content: "# Spec\n\nthe apple orchard section."}},
		"p", "spec.md",
		"# Council verdict: edit\n\nOutcome: the banana wording was tightened by the council.")
	h := &Handlers{Svc: svc, St: s, RAG: searcher}

	// Default (kind omitted → doc) excludes the decision.
	docHits, err := h.SearchKnowledgeBase("p", "banana verdict", 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, hit := range docHits {
		if hit.Kind == rag.KindDecision {
			t.Fatalf("default search leaked a decision: %+v", hit)
		}
	}

	// kind="decision" restricts to decisions.
	decHits, err := h.SearchKnowledgeBaseKind("p", "banana verdict", rag.KindDecision, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(decHits) == 0 {
		t.Fatal("kind=decision returned no hits for an indexed decision")
	}
	for _, hit := range decHits {
		if hit.Kind != rag.KindDecision {
			t.Fatalf("kind=decision returned a non-decision hit: %+v", hit)
		}
	}
}

// TestSearchDecisionsSurviveWhitelist: decisions are project-scoped memory, not
// served files, so they surface even under an active sources whitelist that does
// NOT serve their doc path — while a doc chunk outside the whitelist is filtered.
// This is the production case the single-folder tests miss.
func TestSearchDecisionsSurviveWhitelist(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _, _ string) error { return nil })
	// Restrict to served.md only: hidden.md (which the decision cites) is NOT served.
	svc.SetProjectSources(config.ProjectSources{"p": config.Coverage{Sources: []string{"served.md"}}})
	if !svc.SourcesRestricted() {
		t.Fatal("precondition: the whitelist must be restricted")
	}
	searcher := seedKBWithDecision(t, s,
		[]rag.Doc{{Project: "p", Path: "hidden.md", Content: "# Hidden\n\nthe banana section lives in a hidden doc."}},
		"p", "hidden.md",
		"# Council verdict: edit\n\nOutcome: the banana wording was tightened by the council.")
	h := &Handlers{Svc: svc, St: s, RAG: searcher}

	// A doc hit on the un-served hidden.md is filtered out by the whitelist.
	docHits, err := h.SearchKnowledgeBase("p", "banana", 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, hit := range docHits {
		if hit.DocPath == "hidden.md" {
			t.Fatalf("whitelist did not hide an un-served doc chunk: %+v", hit)
		}
	}

	// The decision about hidden.md STILL returns — memory is not gated by the file
	// whitelist.
	decHits, err := h.SearchKnowledgeBaseKind("p", "banana verdict", rag.KindDecision, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(decHits) == 0 {
		t.Fatal("an active whitelist dropped decision memory — decisions must survive it")
	}
}
