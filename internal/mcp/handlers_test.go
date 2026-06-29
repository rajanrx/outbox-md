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

func TestListOpenCommentsExposesExcerptAndThread(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _ string) error { return nil })
	h := &Handlers{Svc: svc, St: s}
	doc, _, _ := s.CreateDocument("spec.md", "Hello world", "human")
	c, _ := svc.PostComment(doc.ID, domain.Anchor{Start: 6, End: 11}, "human") // anchors "world"
	if _, err := svc.HumanReply(c.ID, "please clarify X"); err != nil {
		t.Fatal(err)
	}

	open, err := h.ListOpenComments()
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 {
		t.Fatalf("open = %d, want 1", len(open))
	}
	oc := open[0]
	if oc.DocPath != "spec.md" {
		t.Errorf("DocPath = %q, want %q", oc.DocPath, "spec.md")
	}
	if oc.Excerpt != "world" {
		t.Errorf("Excerpt = %q, want %q", oc.Excerpt, "world")
	}
	found := false
	for _, m := range oc.Thread {
		if m.Body == "please clarify X" {
			found = true
		}
	}
	if !found {
		t.Errorf("Thread missing human feedback: %+v", oc.Thread)
	}
}

func TestReadDocExposesLifecycle(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _ string) error { return nil })
	h := &Handlers{Svc: svc, St: s}
	doc, _, _ := s.CreateDocument("a.md", "v1", "human")
	_ = s.SetDocumentApproval(doc.ID, doc.CurrentVersionID, domain.DocApproved)

	out, err := h.ReadDoc(doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	d := out["document"].(domain.Document)
	if d.Status != domain.DocApproved || d.ApprovedVersionID != doc.CurrentVersionID {
		t.Errorf("read_doc document = %+v, want approved baseline", d)
	}
}
