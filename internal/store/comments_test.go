package store

import (
	"testing"

	"github.com/rajanrx/outbox-md/internal/domain"
)

func TestCommentAndSuggestionRoundTrip(t *testing.T) {
	s, _ := Open(":memory:")
	defer s.Close()
	doc, v1, _ := s.CreateDocument("spec.md", "hello world", "human")

	c, err := s.CreateComment(domain.Comment{
		DocID: doc.ID, AgainstVersionID: v1.ID,
		Anchor:         domain.Anchor{Start: 6, End: 11},
		AuthorIdentity: "human", Owner: "human", Status: domain.CommentOpen,
	})
	if err != nil {
		t.Fatal(err)
	}
	open, _ := s.ListOpenComments()
	if len(open) != 1 || open[0].ID != c.ID {
		t.Fatalf("open comments = %+v", open)
	}

	if _, err := s.CreateSuggestion(domain.Suggestion{
		CommentID: c.ID, AgainstVersionID: v1.ID,
		ProposedContent: "hello there", State: domain.SuggestionProposed, CreatedBy: "agent",
	}); err != nil {
		t.Fatal(err)
	}
	sg, ok, _ := s.GetSuggestionByComment(c.ID)
	if !ok || sg.ProposedContent != "hello there" {
		t.Fatalf("suggestion = %+v ok=%v", sg, ok)
	}

	if err := s.UpdateCommentAnchor(c.ID, domain.Anchor{Start: 6, End: 11}, domain.CommentDetached); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetComment(c.ID)
	if got.Status != domain.CommentDetached {
		t.Fatalf("status = %s, want detached", got.Status)
	}
}

func TestCommentPostApprovalRoundTrips(t *testing.T) {
	s, _ := Open(":memory:")
	defer s.Close()
	doc, _, _ := s.CreateDocument("a.md", "hi", "human")
	c, err := s.CreateComment(domain.Comment{
		DocID: doc.ID, AgainstVersionID: doc.CurrentVersionID,
		Anchor: domain.Anchor{Start: 0, End: 1}, AuthorIdentity: "human",
		Owner: "human", Status: domain.CommentOpen, PostApproval: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetComment(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.PostApproval {
		t.Error("postApproval = false, want true")
	}
}
