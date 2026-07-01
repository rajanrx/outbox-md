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
	svc := service.New(s, func(_, _, _ string) error { return nil })
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
	svc := service.New(s, func(_, _, _ string) error { return nil })
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
	svc := service.New(s, func(_, _, _ string) error { return nil })
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

// Regression: only TERMINAL comments (resolved/detached) that leave a stale
// still-`proposed` suggestion row behind are excluded from the folder view. A
// non-terminal comment with a live proposed suggestion (e.g. `replied`) is
// covered by TestPendingSuggestionsIncludesNonTerminalProposed.
func TestPendingSuggestionsExcludesLingeringProposedOnTerminal(t *testing.T) {
	for _, status := range []domain.CommentStatus{domain.CommentResolved, domain.CommentDetached} {
		t.Run(string(status), func(t *testing.T) {
			s, _ := store.Open(":memory:")
			defer s.Close()
			svc := service.New(s, func(_, _, _ string) error { return nil })
			h := NewAPI(svc, s, sse.NewHub())

			doc, ver, _ := s.CreateDocument("spec.md", "current", "human")
			c, _ := s.CreateComment(domain.Comment{
				DocID: doc.ID, AgainstVersionID: ver.ID, Anchor: domain.Anchor{Start: 0, End: 3},
				AuthorIdentity: "human", Owner: "human", Status: status,
			})
			// Suggestion stays PROPOSED — the lingering-row case.
			if _, err := s.CreateSuggestion(domain.Suggestion{
				CommentID: c.ID, AgainstVersionID: ver.ID,
				ProposedContent: "x", State: domain.SuggestionProposed, CreatedBy: "agent",
			}); err != nil {
				t.Fatal(err)
			}

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/suggestions/pending", nil))
			var out []store.PendingSuggestion
			_ = json.Unmarshal(rec.Body.Bytes(), &out)
			if len(out) != 0 {
				t.Fatalf("status %q: pending = %v, want empty (terminal comment excluded despite proposed suggestion)", status, out)
			}
		})
	}
}

// A non-terminal comment carrying a live proposed suggestion IS returned by the
// folder view — including `replied`, the reported bug: an agent that both
// proposes a suggestion and replies leaves the comment `replied` while the
// suggestion is still `proposed`, and that diff must still surface. `open`
// covers the pre-address case.
func TestPendingSuggestionsIncludesNonTerminalProposed(t *testing.T) {
	for _, status := range []domain.CommentStatus{domain.CommentReplied, domain.CommentOpen} {
		t.Run(string(status), func(t *testing.T) {
			s, _ := store.Open(":memory:")
			defer s.Close()
			svc := service.New(s, func(_, _, _ string) error { return nil })
			h := NewAPI(svc, s, sse.NewHub())

			doc, ver, _ := s.CreateDocument("spec.md", "current content", "human")
			c, _ := s.CreateComment(domain.Comment{
				DocID: doc.ID, AgainstVersionID: ver.ID, Anchor: domain.Anchor{Start: 0, End: 3},
				AuthorIdentity: "human", Owner: "human", Status: status,
			})
			if _, err := s.CreateSuggestion(domain.Suggestion{
				CommentID: c.ID, AgainstVersionID: ver.ID,
				ProposedContent: "proposed content", State: domain.SuggestionProposed, CreatedBy: "agent",
			}); err != nil {
				t.Fatal(err)
			}

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/suggestions/pending", nil))
			var out []store.PendingSuggestion
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
			if len(out) != 1 || out[0].CommentID != c.ID {
				t.Fatalf("status %q: pending = %v, want exactly the proposed suggestion for comment %s", status, out, c.ID)
			}
		})
	}
}

// GET /api/comments/{id}/suggestion returns the live proposed suggestion
// regardless of comment status — in particular for a `replied` comment (the
// reported bug), so the Card can render the diff alongside the thread.
func TestSuggestionByCommentReturnsProposedForReplied(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _, _ string) error { return nil })
	h := NewAPI(svc, s, sse.NewHub())

	doc, ver, _ := s.CreateDocument("spec.md", "current content", "human")
	c, _ := s.CreateComment(domain.Comment{
		DocID: doc.ID, AgainstVersionID: ver.ID, Anchor: domain.Anchor{Start: 0, End: 7},
		AuthorIdentity: "human", Owner: "human", Status: domain.CommentReplied,
	})
	if _, err := s.CreateSuggestion(domain.Suggestion{
		CommentID: c.ID, AgainstVersionID: ver.ID,
		ProposedContent: "proposed content", State: domain.SuggestionProposed, CreatedBy: "agent",
	}); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/comments/"+c.ID+"/suggestion", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
	}
	var sg domain.Suggestion
	if err := json.Unmarshal(rec.Body.Bytes(), &sg); err != nil {
		t.Fatal(err)
	}
	if sg.State != domain.SuggestionProposed || sg.ProposedContent != "proposed content" {
		t.Fatalf("suggestion = %+v, want proposed with proposed content", sg)
	}
}
