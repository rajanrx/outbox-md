package mcp

import (
	"testing"

	"github.com/rajanrx/outbox-md/internal/config"
	"github.com/rajanrx/outbox-md/internal/domain"
	"github.com/rajanrx/outbox-md/internal/service"
	"github.com/rajanrx/outbox-md/internal/store"
)

func TestHandlersDriveTheLoop(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _, _ string) error { return nil })
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
	svc := service.New(s, func(_, _, _ string) error { return nil })
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

// P1: the sources whitelist must gate the MCP surface too — an agent must not
// see open comments on, or read by id, a doc outside the active whitelist.
func TestMCPSurfaceRespectsSourcesWhitelist(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _, _ string) error { return nil })
	svc.SetConfig(config.Config{Sources: []string{"docs/specs"}})
	h := &Handlers{Svc: svc, St: s}

	inDoc, _, _ := s.CreateDocument("docs/specs/in.md", "hello world", "human")
	outDoc, _, _ := s.CreateDocument("other/out.md", "secret world", "human")
	if _, err := svc.PostComment(inDoc.ID, domain.Anchor{Start: 0, End: 5}, "human"); err != nil {
		t.Fatal(err)
	}
	outC, err := svc.PostComment(outDoc.ID, domain.Anchor{Start: 0, End: 6}, "human")
	if err != nil {
		t.Fatal(err)
	}

	// list_open_comments surfaces only the in-whitelist comment.
	open, err := h.ListOpenComments()
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 || open[0].DocPath != "docs/specs/in.md" {
		t.Fatalf("ListOpenComments = %+v, want only docs/specs/in.md", open)
	}

	// read_doc refuses the out-of-whitelist doc but still serves the in one.
	if _, err := h.ReadDoc(outDoc.ID); err == nil {
		t.Fatal("ReadDoc on out-of-whitelist doc: want error, got nil")
	}
	if _, err := h.ReadDoc(inDoc.ID); err != nil {
		t.Fatalf("ReadDoc on in-whitelist doc: unexpected error %v", err)
	}

	// Write handlers refuse the hidden-doc comment too — no discover-then-mutate.
	if _, err := h.ClaimComment([]string{outC.ID}, "agent"); err == nil {
		t.Fatal("ClaimComment on hidden-doc comment: want error, got nil")
	}
	if err := h.ReplyInThread(outC.ID, "tok", "hi", "agent"); err == nil {
		t.Fatal("ReplyInThread on hidden-doc comment: want error, got nil")
	}
	if _, err := h.ProposeSuggestion(outC.ID, "tok", "x", "agent"); err == nil {
		t.Fatal("ProposeSuggestion on hidden-doc comment: want error, got nil")
	}
}

func TestReadDocExposesLifecycle(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _, _ string) error { return nil })
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
