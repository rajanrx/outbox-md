package mcp

import (
	"context"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rajanrx/outbox-md/internal/config"
	"github.com/rajanrx/outbox-md/internal/rag"
	"github.com/rajanrx/outbox-md/internal/registry"
	"github.com/rajanrx/outbox-md/internal/service"
	"github.com/rajanrx/outbox-md/internal/store"
)

// seedKB builds a rag store over the governance DB (as buildServer does) and
// indexes the given docs keyword-only (no embedder) so the FTS5 floor is what the
// test exercises. Returns a Searcher ready to hand to Handlers.RAG.
func seedKB(t *testing.T, st *store.Store, docs []rag.Doc) *rag.Searcher {
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
	return rag.NewSearcher(rs, nil, ix.Mode)
}

// TestSearchKnowledgeBaseRanksAndScopes: a seeded KB returns ranked hits for a
// query, scoped to the requested project (a same-content doc in another project
// is never returned).
func TestSearchKnowledgeBaseRanksAndScopes(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _, _ string) error { return nil })
	searcher := seedKB(t, s, []rag.Doc{
		{Project: "p1", Path: "apple.md", Content: "# Apple\n\napple apple apple: a crisp sweet fruit."},
		{Project: "p1", Path: "banana.md", Content: "# Banana\n\nbanana: a soft yellow tropical fruit."},
		{Project: "p2", Path: "other.md", Content: "# Apple\n\napple apple apple: a different project entirely."},
	})
	h := &Handlers{Svc: svc, St: s, RAG: searcher}

	hits, err := h.SearchKnowledgeBase("p1", "apple fruit", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("no hits for a query the corpus clearly matches")
	}
	// Top hit is the apple doc; nothing from p2 leaks in (project scoping).
	if hits[0].DocPath != "apple.md" {
		t.Fatalf("top hit = %q, want apple.md", hits[0].DocPath)
	}
	for _, hit := range hits {
		if hit.DocPath == "other.md" {
			t.Fatalf("p2 doc leaked into a p1 search: %+v", hit)
		}
	}
	// Ranked descending by score.
	for i := 1; i < len(hits); i++ {
		if hits[i-1].Score < hits[i].Score {
			t.Fatalf("hits not ranked desc: %v", hits)
		}
	}
}

// TestSearchKnowledgeBaseNoEmbedderStillReturnsFTSHits: with no embedder the tool
// serves the FTS5 keyword floor rather than erroring.
func TestSearchKnowledgeBaseNoEmbedderStillReturnsFTSHits(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _, _ string) error { return nil })
	searcher := seedKB(t, s, []rag.Doc{
		{Project: "", Path: "spec.md", Content: "# Spec\n\nthe lease reaper retries stranded comments."},
	})
	if searcher.Embedder != nil {
		t.Fatal("precondition: this test asserts the no-embedder floor")
	}
	h := &Handlers{Svc: svc, St: s, RAG: searcher}

	hits, err := h.SearchKnowledgeBase("", "reaper retries", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("keyword-only search returned no hits")
	}
}

// TestSearchKnowledgeBaseDefaultsToSoleProject: an omitted project resolves to the
// single registered project, so a per-project agent need not name it.
func TestSearchKnowledgeBaseDefaultsToSoleProject(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _, _ string) error { return nil })
	svc.SetProjects([]registry.Project{{Name: "repo", Root: "/repo", Docs: []string{"."}}})
	searcher := seedKB(t, s, []rag.Doc{
		{Project: "repo", Path: "design.md", Content: "# Design\n\nthe council chair synthesises member verdicts."},
	})
	h := &Handlers{Svc: svc, St: s, RAG: searcher}

	hits, err := h.SearchKnowledgeBase("", "council chair synthesis", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("omitted project did not resolve to the sole registered project")
	}
	if hits[0].DocPath != "design.md" {
		t.Fatalf("top hit = %q, want design.md", hits[0].DocPath)
	}
}

// TestSearchKnowledgeBaseNilRAGIsNoOp: with retrieval unwired the tool returns an
// empty (non-nil) result, never a panic.
func TestSearchKnowledgeBaseNilRAGIsNoOp(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _, _ string) error { return nil })
	h := &Handlers{Svc: svc, St: s}
	hits, err := h.SearchKnowledgeBase("", "anything", 5)
	if err != nil || hits == nil || len(hits) != 0 {
		t.Fatalf("nil RAG: hits=%v err=%v, want empty no-op", hits, err)
	}
}

// TestServerRegistersSearchKnowledgeBase: the tool is discoverable over MCP
// tools/list and callable end-to-end, returning ranked hits from the seeded KB.
func TestServerRegistersSearchKnowledgeBase(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _, _ string) error { return nil })
	svc.SetProjectSources(config.ProjectSources{"": config.Coverage{}})
	searcher := seedKB(t, s, []rag.Doc{
		{Project: "", Path: "spec.md", Content: "# Spec\n\nanchored comments enter the outbox as open."},
	})
	srv := NewServer(&Handlers{Svc: svc, St: s, RAG: searcher})

	ctx := context.Background()
	t1, t2 := mcpsdk.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, t1, nil); err != nil {
		t.Fatal(err)
	}
	c := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := c.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	found := false
	for tool, err := range cs.Tools(ctx, nil) {
		if err != nil {
			t.Fatal(err)
		}
		if tool.Name == "search_knowledge_base" {
			found = true
		}
	}
	if !found {
		t.Fatal("tools/list did not include search_knowledge_base")
	}

	res, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "search_knowledge_base",
		Arguments: map[string]any{"query": "anchored comments outbox", "k": 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("search_knowledge_base call errored: %+v", res.Content)
	}
	out, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("structured content = %T, want object", res.StructuredContent)
	}
	hits, ok := out["hits"].([]any)
	if !ok || len(hits) == 0 {
		t.Fatalf("no hits in tool result: %+v", out)
	}
}
