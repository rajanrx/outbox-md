package service

import (
	"testing"

	"github.com/rajanrx/outbox-md/internal/domain"
	"github.com/rajanrx/outbox-md/internal/store"
)

// claimedComment posts a comment and claims it, returning the comment and token.
func claimedComment(t *testing.T, s *store.Store, svc *Service) (domain.Comment, string) {
	t.Helper()
	doc, _, _ := s.CreateDocument("spec.md", "Hello world", "human")
	c, err := svc.PostComment(doc.ID, domain.Anchor{Start: 6, End: 11}, "human")
	if err != nil {
		t.Fatal(err)
	}
	tok, _, err := svc.Claim([]string{c.ID}, "runner")
	if err != nil {
		t.Fatal(err)
	}
	return c, tok
}

func TestSubmitReviewRequiresValidTokenAndRecordsCandidate(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := New(s, func(_, _, _ string) error { return nil })
	c, tok := claimedComment(t, s, svc)

	// Bad token is rejected.
	if _, err := svc.SubmitReview(c.ID, "wrong", domain.LensCorrectness, domain.VerdictReply, "r", "", "m1"); err == nil {
		t.Fatal("expected invalid-token error")
	}

	// Valid token records a candidate, lazily creating the set.
	cd, err := svc.SubmitReview(c.ID, tok, domain.LensCorrectness, domain.VerdictReply, "looks fine", "", "m1")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if cd.ID == "" || cd.AgentIdentity != "m1" {
		t.Fatalf("candidate = %+v", cd)
	}

	// SubmitReview must NOT resolve or write — the comment is not resolved.
	got, _ := s.GetComment(c.ID)
	if got.Status == domain.CommentResolved {
		t.Error("submit_review must not resolve the comment")
	}

	view, err := svc.ListCandidates(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Candidates) != 1 || view.Set.State != domain.CandidateSetGathering || view.Synthesis != nil {
		t.Fatalf("view = %+v", view)
	}
}

func TestSubmitReviewContentRequiredIffEdit(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := New(s, func(_, _, _ string) error { return nil })
	c, tok := claimedComment(t, s, svc)

	// edit without content → error.
	if _, err := svc.SubmitReview(c.ID, tok, domain.LensCorrectness, domain.VerdictEdit, "r", "", "m1"); err == nil {
		t.Error("edit without content should error")
	}
	// edit with content → ok.
	if _, err := svc.SubmitReview(c.ID, tok, domain.LensCorrectness, domain.VerdictEdit, "r", "Hello there", "m1"); err != nil {
		t.Errorf("edit with content should succeed: %v", err)
	}
	// reply WITH content → error (strict iff).
	if _, err := svc.SubmitReview(c.ID, tok, domain.LensSimplicity, domain.VerdictReply, "r", "stuff", "m2"); err == nil {
		t.Error("reply with content should error")
	}
	// unknown verdict → error.
	if _, err := svc.SubmitReview(c.ID, tok, domain.LensSimplicity, "shrug", "r", "", "m3"); err == nil {
		t.Error("unknown verdict should error")
	}
}

func TestPickCandidateHumanOnlyAndEmitsAcceptEligibleSuggestion(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := New(s, func(_, _, _ string) error { return nil })
	c, tok := claimedComment(t, s, svc)

	edit, err := svc.SubmitReview(c.ID, tok, domain.LensCorrectness, domain.VerdictEdit, "fix", "Hello there", "m1")
	if err != nil {
		t.Fatal(err)
	}

	// A non-human actor cannot pick.
	if _, err := svc.PickCandidate(c.ID, edit.ID, "m1"); err == nil {
		t.Fatal("non-human pick should be rejected")
	}

	// The human picks the edit candidate.
	picked, err := svc.PickCandidate(c.ID, edit.ID, LocalHuman)
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if !picked.Chosen {
		t.Error("picked candidate should be chosen")
	}
	view, _ := svc.ListCandidates(c.ID)
	if view.Set.State != domain.CandidateSetDecided {
		t.Errorf("state = %q, want decided", view.Set.State)
	}

	// The pick emitted an accept-eligible Suggestion — the human accepts via the
	// unchanged path, producing a new version with the candidate's content.
	sg, ok, _ := s.GetSuggestionByComment(c.ID)
	if !ok || sg.State != domain.SuggestionProposed || sg.ProposedContent != "Hello there" {
		t.Fatalf("suggestion = %+v ok=%v", sg, ok)
	}
	ver, err := svc.Accept(c.ID)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if ver.Content != "Hello there" {
		t.Errorf("accepted content = %q", ver.Content)
	}
}

func TestPickRejectsForeignCandidate(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := New(s, func(_, _, _ string) error { return nil })
	c1, tok1 := claimedComment(t, s, svc)

	// A candidate belonging to a different comment's set.
	doc2, _, _ := s.CreateDocument("other.md", "x", "human")
	c2, _ := svc.PostComment(doc2.ID, domain.Anchor{Start: 0, End: 1}, "human")
	tok2, _, _ := svc.Claim([]string{c2.ID}, "runner")
	foreign, _ := svc.SubmitReview(c2.ID, tok2, domain.LensRisk, domain.VerdictReply, "r", "", "m9")

	// Seed c1 with its own set so it exists.
	if _, err := svc.SubmitReview(c1.ID, tok1, domain.LensCorrectness, domain.VerdictReply, "r", "", "m1"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PickCandidate(c1.ID, foreign.ID, LocalHuman); err == nil {
		t.Fatal("picking a foreign candidate should be rejected")
	}
}

func TestRecordSynthesisEmitsSuggestionAndSetsState(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := New(s, func(_, _, _ string) error { return nil })
	c, tok := claimedComment(t, s, svc)
	if _, err := svc.SubmitReview(c.ID, tok, domain.LensCorrectness, domain.VerdictEdit, "fix", "Hello there", "m1"); err != nil {
		t.Fatal(err)
	}

	syn, err := svc.RecordSynthesis(c.ID, "skeptic dissented", "Hello there", "chair", 0.75)
	if err != nil {
		t.Fatalf("synthesis: %v", err)
	}
	if syn.SuggestionID == "" {
		t.Error("synthesis with edit content should link a suggestion")
	}
	view, _ := svc.ListCandidates(c.ID)
	if view.Set.State != domain.CandidateSetSynthesized || view.Synthesis == nil {
		t.Fatalf("view = %+v", view)
	}
	// The synthesised suggestion is accept-eligible (human path unchanged).
	if _, ok, _ := s.GetSuggestionByComment(c.ID); !ok {
		t.Error("expected an emitted suggestion")
	}
}

func TestSubmitReviewRejectsUnknownLens(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := New(s, func(_, _, _ string) error { return nil })
	c, tok := claimedComment(t, s, svc)

	// Unknown lens → rejected (mirrors the strict verdict check).
	if _, err := svc.SubmitReview(c.ID, tok, "garbage", domain.VerdictReply, "r", "", "m1"); err == nil {
		t.Fatal("unknown lens should be rejected")
	}

	// Every defined lens is accepted.
	for _, lens := range []string{
		domain.LensCorrectness, domain.LensCompleteness, domain.LensAmbiguity,
		domain.LensRisk, domain.LensSimplicity, domain.LensSkeptic,
	} {
		if _, err := svc.SubmitReview(c.ID, tok, lens, domain.VerdictReply, "r", "", "m1"); err != nil {
			t.Errorf("lens %q should be accepted: %v", lens, err)
		}
	}
}

func TestPickCandidateRejectsSecondPickAfterDecided(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := New(s, func(_, _, _ string) error { return nil })
	c, tok := claimedComment(t, s, svc)

	first, err := svc.SubmitReview(c.ID, tok, domain.LensCorrectness, domain.VerdictEdit, "fix", "Hello there", "m1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.SubmitReview(c.ID, tok, domain.LensRisk, domain.VerdictEdit, "alt", "Goodbye now", "m2")
	if err != nil {
		t.Fatal(err)
	}

	// First pick decides the set and emits exactly one Suggestion.
	if _, err := svc.PickCandidate(c.ID, first.ID, LocalHuman); err != nil {
		t.Fatalf("first pick: %v", err)
	}

	// A second pick on the now-decided set is rejected — a different candidate…
	if _, err := svc.PickCandidate(c.ID, second.ID, LocalHuman); err == nil {
		t.Fatal("second pick (different candidate) on a decided set should be rejected")
	}
	// …and re-picking the same candidate is rejected too.
	if _, err := svc.PickCandidate(c.ID, first.ID, LocalHuman); err == nil {
		t.Fatal("re-picking the same candidate on a decided set should be rejected")
	}

	// No second Suggestion was emitted — exactly one exists for the comment.
	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM suggestions WHERE comment_id=?`, c.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("suggestion count = %d, want 1", n)
	}

	// And exactly one candidate stayed marked chosen (no double-mark).
	cands, _ := s.ListCandidatesByComment(c.ID)
	chosen := 0
	for _, cd := range cands {
		if cd.Chosen {
			chosen++
		}
	}
	if chosen != 1 {
		t.Fatalf("chosen candidates = %d, want 1", chosen)
	}
}

func TestListCandidatesErrorsWhenNoSet(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := New(s, func(_, _, _ string) error { return nil })
	if _, err := svc.ListCandidates("missing"); err == nil {
		t.Fatal("expected error for comment with no candidate set")
	}
}
