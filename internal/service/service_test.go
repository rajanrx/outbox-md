package service

import (
	"testing"

	"github.com/rajanrx/outbox-md/internal/domain"
	"github.com/rajanrx/outbox-md/internal/store"
)

func TestAcceptRewritesFileAndReanchors(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	var written string
	svc := New(s, func(_, content string) error { written = content; return nil })

	doc, _, _ := s.CreateDocument("spec.md", "Hello world", "human")
	cWorld, _ := svc.PostComment(doc.ID, domain.Anchor{Start: 6, End: 11}, "human") // "world"
	cHello, _ := svc.PostComment(doc.ID, domain.Anchor{Start: 0, End: 5}, "human")  // "Hello"

	tok, _ := svc.Claim([]string{cHello.ID}, "agent")
	if _, err := svc.Propose(cHello.ID, tok, "Say Hello world", "agent"); err != nil {
		t.Fatal(err)
	}
	nv, err := svc.Accept(cHello.ID)
	if err != nil {
		t.Fatal(err)
	}

	if nv.Content != "Say Hello world" || written != "Say Hello world" {
		t.Fatalf("content=%q written=%q", nv.Content, written)
	}
	// The OTHER comment must follow its text from [6,11) to [10,15).
	gotWorld, _ := s.GetComment(cWorld.ID)
	if gotWorld.Anchor != (domain.Anchor{Start: 10, End: 15}) {
		t.Fatalf("world anchor = %+v, want {10,15}", gotWorld.Anchor)
	}
	if gotWorld.Status != domain.CommentOpen {
		t.Fatalf("world status = %s, want open", gotWorld.Status)
	}
	gotHello, _ := s.GetComment(cHello.ID)
	if gotHello.Status != domain.CommentResolved {
		t.Fatalf("hello status = %s, want resolved", gotHello.Status)
	}
}

func TestProposeRejectsBadToken(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := New(s, func(_, _ string) error { return nil })
	doc, _, _ := s.CreateDocument("spec.md", "Hello world", "human")
	c, _ := svc.PostComment(doc.ID, domain.Anchor{Start: 0, End: 5}, "human")
	if _, err := svc.Propose(c.ID, "wrong-token", "x", "agent"); err == nil {
		t.Fatal("expected error for invalid claim token")
	}
}
