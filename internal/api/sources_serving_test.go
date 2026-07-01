package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rajanrx/outbox-md/internal/config"
	"github.com/rajanrx/outbox-md/internal/domain"
	"github.com/rajanrx/outbox-md/internal/service"
	"github.com/rajanrx/outbox-md/internal/sse"
	"github.com/rajanrx/outbox-md/internal/store"
)

// P1 regression: sources must be enforced at SERVE time, not only at import.
// A doc left in the DB from a broader earlier run must not reappear once the
// whitelist is narrowed.
func TestServeRespectsSourcesWhitelist(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _, _ string) error { return nil })
	svc.SetConfig(config.Config{Sources: []string{"docs/specs"}})
	h := NewAPI(svc, s, sse.NewHub())

	inDoc, _, _ := s.CreateDocument("docs/specs/in.md", "a", "import")
	outDoc, _, _ := s.CreateDocument("other/out.md", "b", "import")

	// /api/docs lists only the whitelisted doc.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/docs", nil))
	var docs []domain.Document
	_ = json.Unmarshal(rec.Body.Bytes(), &docs)
	if len(docs) != 1 || docs[0].ID != inDoc.ID {
		t.Fatalf("/api/docs = %+v, want only docs/specs/in.md", docs)
	}

	// Direct access to the out-of-whitelist doc is hidden (404), but preserved.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/docs/"+outDoc.ID, nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET out-of-source doc = %d, want 404", rec.Code)
	}

	// The whitelisted doc is still reachable.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/docs/"+inDoc.ID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET in-source doc = %d, want 200", rec.Code)
	}
}

// P1: comment-scoped endpoints must also honour the whitelist — a stale
// hidden-doc comment id must not read or mutate through /api/comments/{id}/…
func TestCommentScopedEndpointsRespectSources(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _, _ string) error { return nil })
	svc.SetConfig(config.Config{Sources: []string{"docs/specs"}})
	h := NewAPI(svc, s, sse.NewHub())

	inDoc, inVer, _ := s.CreateDocument("docs/specs/in.md", "hello", "human")
	outDoc, outVer, _ := s.CreateDocument("other/out.md", "secret", "human")
	inC, _ := s.CreateComment(domain.Comment{
		DocID: inDoc.ID, AgainstVersionID: inVer.ID, Anchor: domain.Anchor{Start: 0, End: 5},
		AuthorIdentity: "human", Owner: "human", Status: domain.CommentOpen,
	})
	outC, _ := s.CreateComment(domain.Comment{
		DocID: outDoc.ID, AgainstVersionID: outVer.ID, Anchor: domain.Anchor{Start: 0, End: 6},
		AuthorIdentity: "human", Owner: "human", Status: domain.CommentOpen,
	})

	cases := []struct {
		id   string
		want int
	}{
		{outC.ID, http.StatusNotFound}, // hidden doc's comment → 404
		{inC.ID, http.StatusOK},        // whitelisted doc's comment → reachable
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/comments/"+tc.id+"/thread", nil))
		if rec.Code != tc.want {
			t.Fatalf("GET /api/comments/%s/thread = %d, want %d", tc.id, rec.Code, tc.want)
		}
	}
}

// P2: the dev agent endpoints carry comment ids in the body, which the
// path-based route guard can't see — they must enforce the whitelist directly.
func TestDevEndpointsRespectSources(t *testing.T) {
	t.Setenv("OUTBOX_DEV", "1")
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _, _ string) error { return nil })
	svc.SetConfig(config.Config{Sources: []string{"docs/specs"}})
	h := NewAPI(svc, s, sse.NewHub())

	outDoc, outVer, _ := s.CreateDocument("other/out.md", "secret", "human")
	outC, _ := s.CreateComment(domain.Comment{
		DocID: outDoc.ID, AgainstVersionID: outVer.ID, Anchor: domain.Anchor{Start: 0, End: 6},
		AuthorIdentity: "human", Owner: "human", Status: domain.CommentOpen,
	})

	post := func(path, body string) int {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
		return rec.Code
	}
	if code := post("/api/dev/claim", `{"commentIds":["`+outC.ID+`"]}`); code != http.StatusNotFound {
		t.Fatalf("/api/dev/claim on hidden-doc comment = %d, want 404", code)
	}
	if code := post("/api/dev/propose", `{"commentId":"`+outC.ID+`","token":"t","content":"x"}`); code != http.StatusNotFound {
		t.Fatalf("/api/dev/propose on hidden-doc comment = %d, want 404", code)
	}
}

// PR #42 P2 regression: in MULTI-project mode the whitelist must be enforced at
// SERVE time per project, not only at import. A doc imported under a broad config
// and then hidden by narrowing that project's outbox.yaml sources must vanish
// from every surface — the exact #35 regression, multi-mode. This mirrors the
// runtime state after a narrow-then-restart: the excluded doc is already in the
// store, and only the per-project sources guard hides it.
func TestMultiProjectServeRespectsPerProjectSources(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _, _ string) error { return nil })
	// Project "web" is now narrowed to docs/specs — secret.md was imported earlier
	// under a broader config and remains in the store.
	svc.SetProjectSources(config.ProjectSources{
		"web": config.Coverage{Sources: []string{"docs/specs"}},
	})

	inDoc, _, _ := s.CreateDocumentInProject("web", "docs/specs/in.md", "hello", "import")
	secretDoc, secretVer, _ := s.CreateDocumentInProject("web", "secret.md", "top secret", "import")
	secretC, _ := s.CreateComment(domain.Comment{
		DocID: secretDoc.ID, AgainstVersionID: secretVer.ID, Anchor: domain.Anchor{Start: 0, End: 3},
		AuthorIdentity: "human", Owner: "human", Status: domain.CommentOpen,
	})
	h := NewAPI(svc, s, sse.NewHub())

	// /api/docs omits the narrowed-out doc.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/docs", nil))
	var docs []domain.Document
	_ = json.Unmarshal(rec.Body.Bytes(), &docs)
	if len(docs) != 1 || docs[0].ID != inDoc.ID {
		t.Fatalf("/api/docs = %+v, want only docs/specs/in.md", docs)
	}
	// Direct doc read is hidden.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/docs/"+secretDoc.ID, nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET narrowed-out doc = %d, want 404", rec.Code)
	}
	// Its comment thread is hidden.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/comments/"+secretC.ID+"/thread", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET narrowed-out doc's comment thread = %d, want 404", rec.Code)
	}
	// The still-whitelisted doc stays reachable.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/docs/"+inDoc.ID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET whitelisted doc = %d, want 200", rec.Code)
	}
}

// PR #42 P2: two projects with different whitelists must not leak into each
// other — a path served in A but not B (and vice-versa) behaves per-project.
func TestMultiProjectSourcesAreIsolatedPerProject(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _, _ string) error { return nil })
	svc.SetProjectSources(config.ProjectSources{
		"A": config.Coverage{Sources: []string{"specs"}},
		"B": config.Coverage{Sources: []string{"drafts"}},
	})

	// Same relative paths in both projects: each is served by exactly one.
	aIn, _, _ := s.CreateDocumentInProject("A", "specs/x.md", "a-in", "import")   // served in A
	aOut, _, _ := s.CreateDocumentInProject("A", "drafts/y.md", "a-out", "import") // NOT served in A
	bIn, _, _ := s.CreateDocumentInProject("B", "drafts/y.md", "b-in", "import")   // served in B
	bOut, _, _ := s.CreateDocumentInProject("B", "specs/x.md", "b-out", "import")  // NOT served in B
	h := NewAPI(svc, s, sse.NewHub())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/docs", nil))
	var docs []domain.Document
	_ = json.Unmarshal(rec.Body.Bytes(), &docs)
	served := map[string]bool{}
	for _, d := range docs {
		served[d.ID] = true
	}
	if !served[aIn.ID] || !served[bIn.ID] {
		t.Fatalf("/api/docs must include A:specs and B:drafts, got %+v", docs)
	}
	if served[aOut.ID] || served[bOut.ID] {
		t.Fatalf("/api/docs must exclude A:drafts and B:specs (cross-project leak), got %+v", docs)
	}
	// Direct reads confirm per-project gating: same path, opposite verdict.
	for _, tc := range []struct {
		id   string
		want int
	}{
		{aIn.ID, http.StatusOK}, {aOut.ID, http.StatusNotFound},
		{bIn.ID, http.StatusOK}, {bOut.ID, http.StatusNotFound},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/docs/"+tc.id, nil))
		if rec.Code != tc.want {
			t.Fatalf("GET /api/docs/%s = %d, want %d", tc.id, rec.Code, tc.want)
		}
	}
}

// PR #42 P3: an orphaned doc whose project is no longer served (e.g. a removed
// project) is treated as not served — hidden everywhere.
func TestMultiProjectOrphanDocIsHidden(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _, _ string) error { return nil })
	svc.SetProjectSources(config.ProjectSources{
		"live": config.Coverage{}, // serves everything under the live project
	})
	live, _, _ := s.CreateDocumentInProject("live", "a.md", "a", "import")
	orphan, _, _ := s.CreateDocumentInProject("removed", "b.md", "b", "import")
	h := NewAPI(svc, s, sse.NewHub())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/docs", nil))
	var docs []domain.Document
	_ = json.Unmarshal(rec.Body.Bytes(), &docs)
	if len(docs) != 1 || docs[0].ID != live.ID {
		t.Fatalf("/api/docs = %+v, want only the live project's doc", docs)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/docs/"+orphan.ID, nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET orphaned (removed-project) doc = %d, want 404", rec.Code)
	}
}

// P1 (docs-union leak): a project with an EMPTY sources filter still serves ONLY
// the docs it lists — a stale/previously-imported row OUTSIDE the docs list must
// be hidden on EVERY read surface, not just the sources-filtered ones. This is
// the leak the sources-only predicate missed: with no sources, the old code
// served every DB row for the project. Asserts across /api/docs, /api/docs/{id},
// and /api/suggestions/pending; the MCP read/list surface is covered by the
// sibling test in the mcp package.
func TestServeGatesOnDocsUnionWithEmptySources(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _, _ string) error { return nil })
	// Project P serves docs/specs, NO sources filter. under-docs alone must gate.
	svc.SetProjectSources(config.ProjectSources{
		"P": config.Coverage{Docs: []string{"docs/specs"}},
	})

	// In-docs doc (served) and an out-of-docs stale row (must be hidden), each with
	// a comment and a pending suggestion so every surface has something to filter.
	inDoc, inVer, _ := s.CreateDocumentInProject("P", "docs/specs/a.md", "in", "import")
	outDoc, outVer, _ := s.CreateDocumentInProject("P", "other/x.md", "stale", "import")
	inC, _ := s.CreateComment(domain.Comment{
		DocID: inDoc.ID, AgainstVersionID: inVer.ID, Anchor: domain.Anchor{Start: 0, End: 2},
		AuthorIdentity: "human", Owner: "human", Status: domain.CommentOpen,
	})
	outC, _ := s.CreateComment(domain.Comment{
		DocID: outDoc.ID, AgainstVersionID: outVer.ID, Anchor: domain.Anchor{Start: 0, End: 5},
		AuthorIdentity: "human", Owner: "human", Status: domain.CommentOpen,
	})
	_, _ = s.CreateSuggestion(domain.Suggestion{
		CommentID: inC.ID, AgainstVersionID: inVer.ID, ProposedContent: "in2",
		State: domain.SuggestionProposed, CreatedBy: "agent",
	})
	_, _ = s.CreateSuggestion(domain.Suggestion{
		CommentID: outC.ID, AgainstVersionID: outVer.ID, ProposedContent: "stale2",
		State: domain.SuggestionProposed, CreatedBy: "agent",
	})

	// Unit predicate: under-docs gates even with no sources filter.
	if svc.ProjectServes("P", "other/x.md") {
		t.Fatal("ProjectServes(P, other/x.md) = true, want false (outside docs union)")
	}
	if !svc.ProjectServes("P", "docs/specs/a.md") {
		t.Fatal("ProjectServes(P, docs/specs/a.md) = false, want true")
	}

	h := NewAPI(svc, s, sse.NewHub())

	// /api/docs lists only the in-docs doc.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/docs", nil))
	var docs []domain.Document
	_ = json.Unmarshal(rec.Body.Bytes(), &docs)
	if len(docs) != 1 || docs[0].ID != inDoc.ID {
		t.Fatalf("/api/docs = %+v, want only docs/specs/a.md", docs)
	}

	// /api/docs/{id}: out-of-docs is 404, in-docs is 200.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/docs/"+outDoc.ID, nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET out-of-docs doc = %d, want 404", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/docs/"+inDoc.ID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET in-docs doc = %d, want 200", rec.Code)
	}

	// /api/suggestions/pending omits the out-of-docs doc's pending suggestion.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/suggestions/pending", nil))
	var pending []store.PendingSuggestion
	_ = json.Unmarshal(rec.Body.Bytes(), &pending)
	if len(pending) != 1 || pending[0].Path != "docs/specs/a.md" {
		t.Fatalf("/api/suggestions/pending = %+v, want only docs/specs/a.md", pending)
	}
}

// Empty/absent sources serves everything (backward-compatible).
func TestServeEmptySourcesServesAll(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _, _ string) error { return nil })
	h := NewAPI(svc, s, sse.NewHub())

	s.CreateDocument("docs/specs/in.md", "a", "import")
	s.CreateDocument("other/out.md", "b", "import")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/docs", nil))
	var docs []domain.Document
	_ = json.Unmarshal(rec.Body.Bytes(), &docs)
	if len(docs) != 2 {
		t.Fatalf("/api/docs = %d docs, want 2 (no whitelist)", len(docs))
	}
}
