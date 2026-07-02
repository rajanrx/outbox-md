package mcp

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rajanrx/outbox-md/internal/config"
	"github.com/rajanrx/outbox-md/internal/domain"
	"github.com/rajanrx/outbox-md/internal/service"
	"github.com/rajanrx/outbox-md/internal/store"
)

// submit_review is the council-mode sibling of propose_suggestion. It must be
// registered (discoverable via tools/list) and drive Service.SubmitReview.
func TestServerRegistersSubmitReview(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _, _ string) error { return nil })
	srv := NewServer(&Handlers{Svc: svc, St: s})

	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, t1, nil); err != nil {
		t.Fatal(err)
	}
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
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
		if tool.Name == "submit_review" {
			found = true
		}
	}
	if !found {
		t.Fatal("tools/list did not include submit_review")
	}
}

func TestSubmitReviewHandlerDrivesService(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _, _ string) error { return nil })
	h := &Handlers{Svc: svc, St: s}
	doc, _, _ := s.CreateDocument("spec.md", "Hello world", "human")
	c, _ := svc.PostComment(doc.ID, domain.Anchor{Start: 6, End: 11}, "human")
	tok, _, _ := h.ClaimComment([]string{c.ID}, "runner")

	cd, err := h.SubmitReview(c.ID, tok, domain.LensSkeptic, domain.VerdictRejectComment, "premise is wrong", "", "m1")
	if err != nil {
		t.Fatalf("submit_review: %v", err)
	}
	if cd.Lens != domain.LensSkeptic || cd.Verdict != domain.VerdictRejectComment {
		t.Fatalf("candidate = %+v", cd)
	}
	cands, _ := s.ListCandidatesByComment(c.ID)
	if len(cands) != 1 {
		t.Fatalf("candidates = %d, want 1", len(cands))
	}
}

// The council chair's two tools must be registered (discoverable via tools/list).
func TestServerRegistersCouncilChairTools(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _, _ string) error { return nil })
	srv := NewServer(&Handlers{Svc: svc, St: s})

	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, t1, nil); err != nil {
		t.Fatal(err)
	}
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := c.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	want := map[string]bool{"list_candidates": false, "record_synthesis": false}
	for tool, err := range cs.Tools(ctx, nil) {
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := want[tool.Name]; ok {
			want[tool.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Fatalf("tools/list did not include %s", name)
		}
	}
}

// record_synthesis is token-authed: a bad/absent token is rejected; the valid
// claim token records the synthesis, emits the accept-eligible suggestion, and
// persists confidence. list_candidates then returns the members' candidates and
// the recorded synthesis for the chair to read.
func TestRecordSynthesisAndListCandidatesHandlers(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _, _ string) error { return nil })
	h := &Handlers{Svc: svc, St: s}
	doc, _, _ := s.CreateDocument("spec.md", "Hello world", "human")
	c, _ := svc.PostComment(doc.ID, domain.Anchor{Start: 0, End: 5}, "human")
	tok, _, _ := h.ClaimComment([]string{c.ID}, "runner")

	// Two members submit independent candidates.
	if _, err := h.SubmitReview(c.ID, tok, domain.LensCorrectness, domain.VerdictEdit, "fix it", "Howdy world", "m1"); err != nil {
		t.Fatalf("submit m1: %v", err)
	}
	if _, err := h.SubmitReview(c.ID, tok, domain.LensSkeptic, domain.VerdictRejectComment, "premise wrong", "", "m2"); err != nil {
		t.Fatalf("submit m2: %v", err)
	}

	// Bad token is rejected and records nothing.
	if _, err := h.RecordSynthesis(c.ID, "wrong-token", "Howdy world", "m2 dissented", 0.8, 90, "chair"); err == nil {
		t.Fatal("record_synthesis with a bad token: want error, got nil")
	}
	if _, ok, _ := s.GetSynthesisByComment(c.ID); ok {
		t.Fatal("bad-token record_synthesis stored a synthesis")
	}

	// Valid claim token records the verdict and emits the suggestion.
	syn, err := h.RecordSynthesis(c.ID, tok, "Howdy world", "m2 dissented", 0.8, 90, "chair")
	if err != nil {
		t.Fatalf("record_synthesis: %v", err)
	}
	if syn.SuggestionID == "" {
		t.Error("synthesis with edit content should link a suggestion")
	}
	if syn.Confidence != 90 {
		t.Errorf("confidence = %d, want 90", syn.Confidence)
	}
	if _, ok, _ := s.GetSuggestionByComment(c.ID); !ok {
		t.Error("expected an emitted, accept-eligible suggestion")
	}

	// list_candidates returns both members' candidates plus the synthesis.
	view, err := h.ListCandidates(c.ID)
	if err != nil {
		t.Fatalf("list_candidates: %v", err)
	}
	if len(view.Candidates) != 2 {
		t.Fatalf("candidates = %d, want 2", len(view.Candidates))
	}
	if view.Synthesis == nil || view.Synthesis.Confidence != 90 {
		t.Fatalf("synthesis not surfaced with confidence: %+v", view.Synthesis)
	}
}

// The council chair tools honour the sources whitelist: list_candidates and
// record_synthesis refuse a comment whose doc is outside the served set, so a
// stale id can neither read nor mutate a hidden doc.
func TestCouncilChairToolsRespectSourcesWhitelist(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _, _ string) error { return nil })
	svc.SetConfig(config.Config{Sources: []string{"docs/specs"}})
	h := &Handlers{Svc: svc, St: s}

	outDoc, _, _ := s.CreateDocument("other/out.md", "secret world", "human")
	outC, _ := svc.PostComment(outDoc.ID, domain.Anchor{Start: 0, End: 6}, "human")

	if _, err := h.ListCandidates(outC.ID); err == nil {
		t.Fatal("ListCandidates on hidden-doc comment: want error, got nil")
	}
	if _, err := h.RecordSynthesis(outC.ID, "tok", "x", "", 0.5, 50, "chair"); err == nil {
		t.Fatal("RecordSynthesis on hidden-doc comment: want error, got nil")
	}
}
