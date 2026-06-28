package mcp

import (
	"testing"

	"github.com/rajanrx/outbox-md/internal/domain"
	"github.com/rajanrx/outbox-md/internal/service"
	"github.com/rajanrx/outbox-md/internal/store"
)

func TestHandlersDriveTheLoop(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _ string) error { return nil })
	doc, _, _ := s.CreateDocument("spec.md", "Hello world", "human")
	c, _ := svc.PostComment(doc.ID, domain.Anchor{Start: 6, End: 11}, "human")
	h := &Handlers{Svc: svc, St: s}

	open, _ := h.ListOpenComments()
	if len(open) != 1 {
		t.Fatalf("open = %d, want 1", len(open))
	}
	tok, err := h.ClaimComment([]string{c.ID}, "agent")
	if err != nil || tok == "" {
		t.Fatalf("claim: tok=%q err=%v", tok, err)
	}
	sg, err := h.ProposeSuggestion(c.ID, tok, "Hello there", "agent")
	if err != nil || sg.ProposedContent != "Hello there" {
		t.Fatalf("propose: %+v %v", sg, err)
	}
	rd, _ := h.ReadDoc(doc.ID)
	if rd["content"] != "Hello world" {
		t.Fatalf("read_doc content = %v", rd["content"])
	}
}
