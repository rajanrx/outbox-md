package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rajanrx/outbox-md/internal/domain"
	"github.com/rajanrx/outbox-md/internal/service"
	"github.com/rajanrx/outbox-md/internal/sse"
	"github.com/rajanrx/outbox-md/internal/store"
)

func TestPendingSuggestionsEmpty(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _ string) error { return nil })
	h := NewAPI(svc, s, sse.NewHub())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/suggestions/pending", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var out []store.PendingSuggestion
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Fatalf("pending = %v, want empty when no proposals", out)
	}
}

func TestPendingSuggestionsReturnsProposed(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _ string) error { return nil })
	h := NewAPI(svc, s, sse.NewHub())

	// Seed a doc with a comment and a proposed suggestion directly through the
	// store (no claim/propose token dance needed for a read-only assertion).
	doc, ver, _ := s.CreateDocument("spec.md", "current content", "human")
	c, err := s.CreateComment(domain.Comment{
		DocID: doc.ID, AgainstVersionID: ver.ID,
		Anchor: domain.Anchor{Start: 0, End: 7}, AuthorIdentity: "human",
		Owner: "human", Status: domain.CommentAddressed,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSuggestion(domain.Suggestion{
		CommentID: c.ID, AgainstVersionID: ver.ID,
		ProposedContent: "proposed content", State: domain.SuggestionProposed, CreatedBy: "agent",
	}); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/suggestions/pending", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
	}
	var out []store.PendingSuggestion
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("pending = %v, want exactly one", out)
	}
	got := out[0]
	if got.Path != "spec.md" || got.CommentID != c.ID ||
		got.Current != "current content" || got.Proposed != "proposed content" {
		t.Fatalf("pending[0] = %+v, want {path spec.md, current/proposed content}", got)
	}
}

// A suggestion that is no longer proposed (accepted/rejected) must be excluded.
func TestPendingSuggestionsExcludesResolved(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _ string) error { return nil })
	h := NewAPI(svc, s, sse.NewHub())

	doc, ver, _ := s.CreateDocument("spec.md", "current", "human")
	c, _ := s.CreateComment(domain.Comment{
		DocID: doc.ID, AgainstVersionID: ver.ID, Anchor: domain.Anchor{Start: 0, End: 3},
		AuthorIdentity: "human", Owner: "human", Status: domain.CommentResolved,
	})
	sg, _ := s.CreateSuggestion(domain.Suggestion{
		CommentID: c.ID, AgainstVersionID: ver.ID,
		ProposedContent: "x", State: domain.SuggestionProposed, CreatedBy: "agent",
	})
	if err := s.UpdateSuggestionState(sg.ID, domain.SuggestionAccepted); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/suggestions/pending", nil))
	var out []store.PendingSuggestion
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if len(out) != 0 {
		t.Fatalf("pending = %v, want empty (accepted suggestion excluded)", out)
	}
}
