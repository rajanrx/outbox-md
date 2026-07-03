package service

import (
	"strings"
	"testing"

	"github.com/rajanrx/outbox-md/internal/domain"
	"github.com/rajanrx/outbox-md/internal/store"
)

// TestDecisionRecordedOnSynthesisAndAccept: a chair synthesis (verdict) and the
// subsequent Accept (accepted change) each fire the decision-memory sink with a
// record carrying the doc path, the flagged excerpt, and the outcome (§5.4).
func TestDecisionRecordedOnSynthesisAndAccept(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := New(s, func(_, _, _ string) error { return nil })

	var recs []DecisionRecord
	svc.SetDecisionRecorder(func(r DecisionRecord) { recs = append(recs, r) })

	c, tok := claimedComment(t, s, svc)
	if _, err := svc.SubmitReview(c.ID, tok, 0, domain.LensCorrectness, domain.VerdictEdit, "fix", "Hello there", "m1"); err != nil {
		t.Fatal(err)
	}
	// Chair synthesis with an edit → first decision record (a verdict).
	if _, err := svc.RecordSynthesis(c.ID, tok, "skeptic dissented", "Hello there", "chair", 0.75, 80); err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("synthesis did not record exactly one decision; got %d", len(recs))
	}
	syn := recs[0]
	if syn.DocPath != "spec.md" {
		t.Fatalf("synthesis record doc path = %q, want spec.md", syn.DocPath)
	}
	if !strings.Contains(syn.Outcome, "edit") {
		t.Fatalf("synthesis outcome missing edit verdict: %q", syn.Outcome)
	}
	if syn.Excerpt == "" {
		t.Fatal("synthesis record carried no comment excerpt")
	}
	if syn.Rationale != "skeptic dissented" {
		t.Fatalf("synthesis rationale = %q, want the dissent", syn.Rationale)
	}

	// Accept the emitted suggestion → second decision record (an accepted change).
	if _, err := svc.Accept(c.ID); err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("accept did not record a decision; total records = %d", len(recs))
	}
	acc := recs[1]
	if acc.DocPath != "spec.md" {
		t.Fatalf("accept record doc path = %q, want spec.md", acc.DocPath)
	}
	if !strings.Contains(strings.ToLower(acc.Outcome), "accepted") {
		t.Fatalf("accept outcome missing 'accepted': %q", acc.Outcome)
	}
}

// TestDecisionSinkFailureNeverBlocks: a sink that panics (e.g. a rag index error
// bubbling up) must NOT fail the verdict or the accept — decision memory is
// best-effort by contract (§5.4).
func TestDecisionSinkFailureNeverBlocks(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := New(s, func(_, _, _ string) error { return nil })
	svc.SetDecisionRecorder(func(DecisionRecord) { panic("rag index blew up") })

	c, tok := claimedComment(t, s, svc)
	if _, err := svc.SubmitReview(c.ID, tok, 0, domain.LensCorrectness, domain.VerdictEdit, "fix", "Hello there", "m1"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RecordSynthesis(c.ID, tok, "", "Hello there", "chair", 0.9, 90); err != nil {
		t.Fatalf("a failing decision sink blocked the synthesis: %v", err)
	}
	if _, err := svc.Accept(c.ID); err != nil {
		t.Fatalf("a failing decision sink blocked the accept: %v", err)
	}
}
