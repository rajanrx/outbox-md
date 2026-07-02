package service

import (
	"testing"

	"github.com/rajanrx/outbox-md/internal/domain"
	"github.com/rajanrx/outbox-md/internal/store"
)

// TestHumanReplyReEngagesCouncil: a human "refine" reply on a comment whose council
// already synthesized resets the set to gathering, so the re-triggered council can
// record a FRESH verdict. Without the reset, the single-shot TryClaimSynthesis
// guard permanently blocks re-synthesis after the first — the refine re-triggers
// but produces nothing.
func TestHumanReplyReEngagesCouncil(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := New(s, func(_, _, _ string) error { return nil })
	c, tok := claimedComment(t, s, svc)
	if _, err := svc.SubmitReview(c.ID, tok, domain.LensCorrectness, domain.VerdictEdit, "fix", "Hello there", "m1"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RecordSynthesis(c.ID, tok, "", "Hello there", "chair", 0.9, 90); err != nil {
		t.Fatalf("first synthesis: %v", err)
	}
	// Baseline: a second synthesis is blocked while the set is synthesized.
	if _, err := svc.RecordSynthesis(c.ID, tok, "", "Hello again", "chair", 0.9, 90); err == nil {
		t.Fatal("second synthesis should be blocked before re-engagement")
	}

	// The human re-engages (a refine reply).
	if _, err := svc.HumanReply(c.ID, "actually, tighten it"); err != nil {
		t.Fatal(err)
	}
	if set, ok, _ := s.GetCandidateSetByComment(c.ID); !ok || set.State != domain.CandidateSetGathering {
		t.Fatalf("set state = %q ok=%v, want gathering after re-engagement", set.State, ok)
	}
	// A fresh verdict now succeeds. Re-claim (HumanReply reopened the comment).
	tok2, won, err := svc.Claim([]string{c.ID}, "council")
	if err != nil || len(won) != 1 {
		t.Fatalf("re-claim won=%v err=%v", won, err)
	}
	if _, err := svc.RecordSynthesis(c.ID, tok2, "", "Hello again", "chair", 0.8, 80); err != nil {
		t.Fatalf("re-synthesis after re-engagement should succeed: %v", err)
	}
}

// TestHumanReplyNoCandidateSetIsNoop: a human reply on a plain (single-agent)
// comment with no council set must not error — the re-engagement reset is a no-op.
func TestHumanReplyNoCandidateSetIsNoop(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := New(s, func(_, _, _ string) error { return nil })
	doc, _, _ := s.CreateDocument("spec.md", "hello world", "human")
	c, _ := svc.PostComment(doc.ID, domain.Anchor{Start: 0, End: 5}, "human")
	if _, err := svc.HumanReply(c.ID, "a plain reply"); err != nil {
		t.Fatalf("HumanReply on a set-less comment should not error: %v", err)
	}
}

// TestRefineSurfacesNewestSuggestion: the single-agent refine flow — propose, human
// refine reply, re-claim, re-propose — must surface the NEWEST suggestion, even when
// both rows share a same-second created_at (the rowid DESC tiebreak).
func TestRefineSurfacesNewestSuggestion(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := New(s, func(_, _, _ string) error { return nil })
	c, tok := claimedComment(t, s, svc)
	if _, err := svc.Propose(c.ID, tok, "first draft", "agent"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.HumanReply(c.ID, "refine: tighten it"); err != nil {
		t.Fatal(err)
	}
	tok2, won, err := svc.Claim([]string{c.ID}, "agent")
	if err != nil || len(won) != 1 {
		t.Fatalf("re-claim won=%v err=%v", won, err)
	}
	if _, err := svc.Propose(c.ID, tok2, "second draft", "agent"); err != nil {
		t.Fatalf("re-propose after refine should succeed: %v", err)
	}
	if sg, ok, _ := s.GetSuggestionByComment(c.ID); !ok || sg.ProposedContent != "second draft" {
		t.Fatalf("suggestion = %q ok=%v, want the newest 'second draft'", sg.ProposedContent, ok)
	}
}
