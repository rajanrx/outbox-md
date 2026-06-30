package store

import (
	"testing"

	"github.com/rajanrx/outbox-md/internal/domain"
)

func TestCandidateSetRoundTrip(t *testing.T) {
	s, _ := Open(":memory:")
	defer s.Close()
	doc, v1, _ := s.CreateDocument("spec.md", "hello world", "human")
	c, _ := s.CreateComment(domain.Comment{
		DocID: doc.ID, AgainstVersionID: v1.ID, Anchor: domain.Anchor{Start: 6, End: 11},
		AuthorIdentity: "human", Owner: "human", Status: domain.CommentClaimed,
	})

	// The set is created lazily and is the SAME set on a second call (one per comment).
	set, err := s.GetOrCreateCandidateSet(c.ID)
	if err != nil || set.ID == "" {
		t.Fatalf("get-or-create set: %+v err=%v", set, err)
	}
	if set.State != domain.CandidateSetGathering {
		t.Errorf("state = %q, want gathering", set.State)
	}
	set2, err := s.GetOrCreateCandidateSet(c.ID)
	if err != nil || set2.ID != set.ID {
		t.Fatalf("second get-or-create returned a different set: %v vs %v (err=%v)", set2.ID, set.ID, err)
	}

	// Candidates list in submission order.
	for _, m := range []struct{ lens, verdict, content, agent string }{
		{domain.LensCorrectness, domain.VerdictEdit, "hello there", "agent-1"},
		{domain.LensSkeptic, domain.VerdictRejectComment, "", "agent-2"},
		{domain.LensSimplicity, domain.VerdictReply, "", "agent-3"},
	} {
		if _, err := s.AddCandidate(domain.Candidate{
			CandidateSetID: set.ID, Lens: m.lens, Verdict: m.verdict,
			Rationale: "because", Content: m.content, AgentIdentity: m.agent,
		}); err != nil {
			t.Fatal(err)
		}
	}
	cands, err := s.ListCandidatesByComment(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 3 {
		t.Fatalf("candidates = %d, want 3", len(cands))
	}
	gotAgents := []string{cands[0].AgentIdentity, cands[1].AgentIdentity, cands[2].AgentIdentity}
	want := []string{"agent-1", "agent-2", "agent-3"}
	for i := range want {
		if gotAgents[i] != want[i] {
			t.Fatalf("order = %v, want %v", gotAgents, want)
		}
	}
	if cands[0].Verdict != domain.VerdictEdit || cands[0].Content != "hello there" {
		t.Errorf("candidate 0 = %+v", cands[0])
	}

	// Mark chosen + flip state round-trips.
	if err := s.MarkCandidateChosen(cands[0].ID); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetCandidate(cands[0].ID)
	if !got.Chosen {
		t.Error("chosen = false, want true")
	}
	if err := s.SetCandidateSetState(set.ID, domain.CandidateSetDecided); err != nil {
		t.Fatal(err)
	}
	set, _, _ = s.GetCandidateSetByComment(c.ID)
	if set.State != domain.CandidateSetDecided {
		t.Errorf("state = %q, want decided", set.State)
	}

	// Synthesis records and reads back.
	if _, err := s.RecordSynthesis(domain.Synthesis{
		CandidateSetID: set.ID, AgreementScore: 0.66, Dissent: "skeptic disagreed",
		SuggestionID: "sg-1", CreatedBy: "chair",
	}); err != nil {
		t.Fatal(err)
	}
	syn, ok, err := s.GetSynthesisByComment(c.ID)
	if err != nil || !ok {
		t.Fatalf("get synthesis: ok=%v err=%v", ok, err)
	}
	if syn.AgreementScore != 0.66 || syn.Dissent != "skeptic disagreed" || syn.SuggestionID != "sg-1" {
		t.Errorf("synthesis = %+v", syn)
	}
}

func TestCandidateSetMissingReturnsNotOK(t *testing.T) {
	s, _ := Open(":memory:")
	defer s.Close()
	_, ok, err := s.GetCandidateSetByComment("nope")
	if err != nil || ok {
		t.Fatalf("expected (notfound, no error), got ok=%v err=%v", ok, err)
	}
}

func TestDecisionLogIncludesCandidateAndSynthesis(t *testing.T) {
	s, _ := Open(":memory:")
	defer s.Close()
	doc, v1, _ := s.CreateDocument("spec.md", "hello world", "human")
	c, _ := s.CreateComment(domain.Comment{
		DocID: doc.ID, AgainstVersionID: v1.ID, Anchor: domain.Anchor{Start: 6, End: 11},
		AuthorIdentity: "human", Owner: "human", Status: domain.CommentClaimed,
	})
	set, _ := s.GetOrCreateCandidateSet(c.ID)
	if _, err := s.AddCandidate(domain.Candidate{
		CandidateSetID: set.ID, Lens: domain.LensSkeptic, Verdict: domain.VerdictRejectComment,
		AgentIdentity: "agent-2",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordSynthesis(domain.Synthesis{
		CandidateSetID: set.ID, AgreementScore: 0.5, Dissent: "noted", CreatedBy: "chair",
	}); err != nil {
		t.Fatal(err)
	}

	log, err := s.ListDecisionLog(doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]domain.LogEntry{}
	for _, e := range log {
		kinds[e.Kind] = e
	}
	cand, ok := kinds["candidate"]
	if !ok {
		t.Fatalf("log missing candidate entry: %+v", log)
	}
	if cand.Actor != "agent-2" || cand.Detail != "skeptic: reject_comment" {
		t.Errorf("candidate entry = %+v", cand)
	}
	syn, ok := kinds["synthesis"]
	if !ok {
		t.Fatalf("log missing synthesis entry: %+v", log)
	}
	if syn.Actor != "chair" || syn.Detail != "noted" {
		t.Errorf("synthesis entry = %+v", syn)
	}
	// Ordering: created < comment < candidate < synthesis.
	idx := map[string]int{}
	for i, e := range log {
		if _, seen := idx[e.Kind]; !seen {
			idx[e.Kind] = i
		}
	}
	if !(idx["created"] < idx["comment"] && idx["comment"] < idx["candidate"] && idx["candidate"] < idx["synthesis"]) {
		t.Errorf("ordering wrong: %v", idx)
	}
}
