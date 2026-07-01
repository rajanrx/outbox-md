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

// seedCandidate posts+claims a comment and submits one edit review, returning
// the comment id and the candidate id.
func seedCandidate(t *testing.T, s *store.Store, svc *service.Service) (string, string) {
	t.Helper()
	doc, _, _ := s.CreateDocument("spec.md", "Hello world", "human")
	c, err := svc.PostComment(doc.ID, domain.Anchor{Start: 6, End: 11}, "human")
	if err != nil {
		t.Fatal(err)
	}
	tok, err := svc.Claim([]string{c.ID}, "runner")
	if err != nil {
		t.Fatal(err)
	}
	cd, err := svc.SubmitReview(c.ID, tok, domain.LensCorrectness, domain.VerdictEdit, "fix", "Hello there", "m1")
	if err != nil {
		t.Fatal(err)
	}
	return c.ID, cd.ID
}

func TestGetCandidatesEndpoint(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _ string) error { return nil })
	h := NewAPI(svc, s, sse.NewHub(), nil)
	commentID, candID := seedCandidate(t, s, svc)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/comments/"+commentID+"/candidates", nil))
	if rec.Code != 200 {
		t.Fatalf("GET candidates: %d %s", rec.Code, rec.Body.String())
	}
	var view service.CouncilView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Candidates) != 1 || view.Candidates[0].ID != candID {
		t.Fatalf("view = %+v", view)
	}
	if view.Set.State != domain.CandidateSetGathering {
		t.Errorf("state = %q, want gathering", view.Set.State)
	}

	// Unknown comment → 404.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/comments/nope/candidates", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown comment: %d", rec.Code)
	}
}

func TestPickCandidateEndpoint(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _ string) error { return nil })
	h := NewAPI(svc, s, sse.NewHub(), nil)
	commentID, candID := seedCandidate(t, s, svc)

	// Happy path: the (server-set) human picks the edit candidate.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/comments/"+commentID+"/candidates/"+candID+"/pick", nil))
	if rec.Code != 200 {
		t.Fatalf("pick: %d %s", rec.Code, rec.Body.String())
	}
	var cand domain.Candidate
	_ = json.Unmarshal(rec.Body.Bytes(), &cand)
	if !cand.Chosen {
		t.Errorf("picked candidate not chosen: %+v", cand)
	}
	// The pick emitted an accept-eligible suggestion.
	if _, ok, _ := s.GetSuggestionByComment(commentID); !ok {
		t.Error("expected an emitted suggestion after pick")
	}

	// Other path: picking an unknown candidate → 400.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/comments/"+commentID+"/candidates/missing/pick", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown candidate pick: %d %s", rec.Code, rec.Body.String())
	}
}
