package store

import (
	"testing"

	"github.com/rajanrx/outbox-md/internal/domain"
)

func TestListDecisionLog(t *testing.T) {
	s, _ := Open(":memory:")
	defer s.Close()

	doc, v1, _ := s.CreateDocument("spec.md", "hello world base", "human") // v1 = created
	if _, err := s.CreateComment(domain.Comment{
		DocID: doc.ID, AgainstVersionID: v1.ID, Anchor: domain.Anchor{Start: 6, End: 11}, // "world"
		AuthorIdentity: "human", Owner: "human", Status: domain.CommentOpen,
	}); err != nil {
		t.Fatal(err)
	}
	c, _ := s.GetComment(firstCommentID(t, s, doc.ID))
	if _, err := s.CreateSuggestion(domain.Suggestion{
		CommentID: c.ID, AgainstVersionID: v1.ID, ProposedContent: "hello world v2",
		State: domain.SuggestionProposed, CreatedBy: "agent",
	}); err != nil {
		t.Fatal(err)
	}
	v2, _ := s.AddVersion(doc.ID, "hello world v2", "agent") // edit
	if _, err := s.CreateApproval(domain.Approval{DocID: doc.ID, VersionID: v2.ID, ApprovedBy: "human", Note: "ok"}); err != nil {
		t.Fatal(err)
	}
	v3, _ := s.AddVersion(doc.ID, "hello world v3", "agent") // amend edit
	if _, err := s.CreateApproval(domain.Approval{DocID: doc.ID, VersionID: v3.ID, ApprovedBy: "human", Note: "amended"}); err != nil {
		t.Fatal(err)
	}

	log, err := s.ListDecisionLog(doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Same-second events sort by kind-priority: created < comment < proposal < edit < approval.
	kinds := []string{}
	for _, e := range log {
		kinds = append(kinds, e.Kind)
	}
	want := []string{"created", "comment", "proposal", "edit", "edit", "approval", "approval"}
	if len(kinds) != len(want) {
		t.Fatalf("kinds = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("kinds = %v, want %v", kinds, want)
		}
	}
	// Field checks.
	if log[0].Version != 1 || log[0].Actor != "human" {
		t.Errorf("created entry = %+v", log[0])
	}
	if log[1].Detail != "world" {
		t.Errorf("comment excerpt = %q, want world", log[1].Detail)
	}
	if log[5].Kind != "approval" || log[5].Version != 2 || log[5].Detail != "ok" || log[5].ReApproval {
		t.Errorf("first approval = %+v, want v2 note=ok reApproval=false", log[5])
	}
	if log[6].Version != 3 || !log[6].ReApproval {
		t.Errorf("second approval = %+v, want v3 reApproval=true", log[6])
	}
}

// firstCommentID returns the only comment's id for a doc (test helper).
func firstCommentID(t *testing.T, s *Store, docID string) string {
	t.Helper()
	cs, err := s.ListComments(docID)
	if err != nil || len(cs) == 0 {
		t.Fatalf("no comments: %v", err)
	}
	return cs[0].ID
}
