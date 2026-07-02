package store

import (
	"database/sql"
	"testing"

	"github.com/rajanrx/outbox-md/internal/domain"
)

// discussionFixture posts a claimed comment and returns its lazily-created set.
func discussionFixture(t *testing.T, s *Store) (domain.Comment, domain.CandidateSet) {
	t.Helper()
	doc, v1, _ := s.CreateDocument("spec.md", "hello world", "human")
	c, err := s.CreateComment(domain.Comment{
		DocID: doc.ID, AgainstVersionID: v1.ID, Anchor: domain.Anchor{Start: 6, End: 11},
		AuthorIdentity: "human", Owner: "human", Status: domain.CommentClaimed,
	})
	if err != nil {
		t.Fatal(err)
	}
	set, err := s.GetOrCreateCandidateSet(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	return c, set
}

// TestCandidateRoundRoundTrips: a candidate's round persists and reads back
// through both AddCandidate/GetCandidate and ListCandidatesByComment.
func TestCandidateRoundRoundTrips(t *testing.T) {
	s, _ := Open(":memory:")
	defer s.Close()
	c, set := discussionFixture(t, s)

	r0, err := s.AddCandidate(domain.Candidate{
		CandidateSetID: set.ID, Round: 0, Lens: domain.LensCorrectness,
		Verdict: domain.VerdictReply, Rationale: "take", AgentIdentity: "m1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddCandidate(domain.Candidate{
		CandidateSetID: set.ID, Round: 2, Lens: domain.LensRisk,
		Verdict: domain.VerdictReply, Rationale: "revised", AgentIdentity: "m1",
	}); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetCandidate(r0.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Round != 0 {
		t.Fatalf("round-0 candidate round = %d, want 0", got.Round)
	}
	cands, err := s.ListCandidatesByComment(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 2 {
		t.Fatalf("candidates = %d, want 2", len(cands))
	}
	if cands[0].Round != 0 || cands[1].Round != 2 {
		t.Fatalf("rounds = [%d %d], want [0 2]", cands[0].Round, cands[1].Round)
	}
}

// TestDiscussionMessageAddAndListOrdered: messages read back ordered by round,
// then insertion order within a round, and Refs (JSON) round-trip for both empty
// and populated citations.
func TestDiscussionMessageAddAndListOrdered(t *testing.T) {
	s, _ := Open(":memory:")
	defer s.Close()
	c, set := discussionFixture(t, s)

	// Insert OUT of round order to prove the ORDER BY round sorts, not insertion.
	if _, err := s.AddDiscussionMessage(domain.DiscussionMessage{
		CandidateSetID: set.ID, Round: 2, AgentIdentity: "m2", Body: "round2-first",
		Refs: []domain.DiscussionRef{{Kind: "member", TargetID: "m1"}, {Kind: "kb", TargetID: "doc:spec.md#2"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddDiscussionMessage(domain.DiscussionMessage{
		CandidateSetID: set.ID, Round: 1, AgentIdentity: "m1", Body: "round1-a",
	}); err != nil { // nil refs → "[]"
		t.Fatal(err)
	}
	if _, err := s.AddDiscussionMessage(domain.DiscussionMessage{
		CandidateSetID: set.ID, Round: 1, AgentIdentity: "m2", Body: "round1-b",
		Refs: []domain.DiscussionRef{},
	}); err != nil {
		t.Fatal(err)
	}

	msgs, err := s.ListDiscussionByComment(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 {
		t.Fatalf("messages = %d, want 3", len(msgs))
	}
	// round 1 (a, b in insertion order) then round 2.
	gotBodies := []string{msgs[0].Body, msgs[1].Body, msgs[2].Body}
	want := []string{"round1-a", "round1-b", "round2-first"}
	for i := range want {
		if gotBodies[i] != want[i] {
			t.Fatalf("order = %v, want %v", gotBodies, want)
		}
	}
	// Empty-refs messages come back as a non-nil empty slice (serialises as []).
	if msgs[0].Refs == nil || len(msgs[0].Refs) != 0 {
		t.Fatalf("round1-a refs = %+v, want non-nil empty", msgs[0].Refs)
	}
	// Populated refs round-trip in order.
	r := msgs[2].Refs
	if len(r) != 2 || r[0].Kind != "member" || r[0].TargetID != "m1" || r[1].Kind != "kb" || r[1].TargetID != "doc:spec.md#2" {
		t.Fatalf("round2 refs = %+v, want the two citations", r)
	}
}

// TestListDiscussionEmptyIsNonNil: a set with no messages returns [] not nil, so
// CouncilView.Discussion serialises as an empty array.
func TestListDiscussionEmptyIsNonNil(t *testing.T) {
	s, _ := Open(":memory:")
	defer s.Close()
	c, _ := discussionFixture(t, s)
	msgs, err := s.ListDiscussionByComment(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if msgs == nil {
		t.Fatal("empty discussion should be a non-nil slice")
	}
	if len(msgs) != 0 {
		t.Fatalf("messages = %d, want 0", len(msgs))
	}
}

// TestMigrateAddsRoundToLegacyCandidates mirrors the confidence migration test:
// an OLD-shape candidates table (no round) gains the column via migrate(), legacy
// rows default to 0, and a second migrate() is a no-op (duplicate column ignored).
// discussion_messages, being created by schema.sql on every Open, is proven by
// the round-trip tests above rather than the ALTER path.
func TestMigrateAddsRoundToLegacyCandidates(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, ddl := range []string{
		`CREATE TABLE documents (id TEXT PRIMARY KEY, path TEXT)`,
		`CREATE TABLE comments (id TEXT PRIMARY KEY)`,
		`CREATE TABLE syntheses (id TEXT PRIMARY KEY)`,
		// Original pre-round shape.
		`CREATE TABLE candidates (id TEXT PRIMARY KEY, lens TEXT)`,
		`INSERT INTO candidates(id) VALUES('cand-legacy')`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	if err := migrate(db); err != nil {
		t.Fatalf("migrate on legacy DB: %v", err)
	}
	var round int
	if err := db.QueryRow(`SELECT round FROM candidates WHERE id='cand-legacy'`).Scan(&round); err != nil {
		t.Fatalf("round column missing after migrate: %v", err)
	}
	if round != 0 {
		t.Fatalf("legacy row round = %d, want 0", round)
	}
	if err := migrate(db); err != nil {
		t.Fatalf("re-migrate on existing DB: %v", err)
	}
}
