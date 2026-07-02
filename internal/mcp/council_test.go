package mcp

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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
