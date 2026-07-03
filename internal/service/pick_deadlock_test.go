package service

import (
	"strings"
	"testing"

	"github.com/rajanrx/outbox-md/internal/domain"
	"github.com/rajanrx/outbox-md/internal/store"
)

// TestPickNonEditRejectsStaleChairSuggestion (PR #84 P1): in a deadlock the chair's
// low-confidence synthesis has already emitted a proposed EDIT suggestion. If the
// human then picks a NON-EDIT candidate (reply/reject), the stale edit must NOT
// remain proposed — otherwise the card would render an ordinary Accept/Reject diff
// for a change the human explicitly did not pick.
func TestPickNonEditRejectsStaleChairSuggestion(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := New(s, func(_, _, _ string) error { return nil })
	c, tok := claimedComment(t, s, svc)

	// A reply candidate (what the human will pick) + the chair's low-confidence edit
	// synthesis (which emits a proposed edit suggestion).
	reply, err := svc.SubmitReview(c.ID, tok, 0, domain.LensCorrectness, domain.VerdictReply, "prefer no change", "", "m1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RecordSynthesis(c.ID, tok, "", "Hello EDITED", "", "chair", 0.3, 30); err != nil {
		t.Fatalf("synthesis: %v", err)
	}
	if sg, ok, _ := s.GetSuggestionByComment(c.ID); !ok || sg.State != domain.SuggestionProposed {
		t.Fatalf("expected a proposed chair edit suggestion, got %+v ok=%v", sg, ok)
	}

	// Human picks the NON-EDIT candidate.
	if _, err := svc.PickCandidate(c.ID, reply.ID, LocalHuman); err != nil {
		t.Fatalf("pick: %v", err)
	}
	// The stale edit suggestion must no longer be proposed (so the UI, which is
	// interactive only while proposed, won't offer Accept/Reject for it).
	if sg, ok, _ := s.GetSuggestionByComment(c.ID); !ok || sg.State == domain.SuggestionProposed {
		t.Fatalf("after a non-edit pick the chair's stale edit must not remain proposed; got %+v", sg)
	}
	// The picked no-edit outcome must be RECORDED + the comment moved out of the
	// 'addressed' state the chair edit left it in — not stranded silently.
	got, _ := s.GetComment(c.ID)
	if got.Status != domain.CommentReplied {
		t.Fatalf("status = %q, want replied (out of addressed) after a non-edit pick", got.Status)
	}
	th, _ := s.ListThread(c.ID)
	if len(th) == 0 || !strings.Contains(th[len(th)-1].Body, "Council pick") || !strings.Contains(th[len(th)-1].Body, "m1") {
		t.Fatalf("expected a visible 'Council pick' reply naming the picked member; thread=%+v", th)
	}
}

// TestPickEditEmitsFreshSuggestion: picking an EDIT candidate still emits its
// suggestion as the proposed one (the pick's edit becomes accept-eligible) — the
// reject-then-emit ordering must not swallow it.
func TestPickEditEmitsFreshSuggestion(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := New(s, func(_, _, _ string) error { return nil })
	c, tok := claimedComment(t, s, svc)
	edit, err := svc.SubmitReview(c.ID, tok, 0, domain.LensCorrectness, domain.VerdictEdit, "fix", "Hello there", "m1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PickCandidate(c.ID, edit.ID, LocalHuman); err != nil {
		t.Fatalf("pick: %v", err)
	}
	if sg, ok, _ := s.GetSuggestionByComment(c.ID); !ok || sg.State != domain.SuggestionProposed || sg.ProposedContent != "Hello there" {
		t.Fatalf("edit pick must emit a proposed suggestion with the picked content; got %+v ok=%v", sg, ok)
	}
}
