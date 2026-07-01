package store

import (
	"testing"
	"time"

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

// TestProcessingUntilRoundTrip covers set → scan → clear on the ephemeral
// processing hint, and that a fresh comment scans as nil (not-processing).
func TestProcessingUntilRoundTrip(t *testing.T) {
	s, _ := Open(":memory:")
	defer s.Close()
	doc, _, _ := s.CreateDocument("a.md", "hi", "human")
	c, err := s.CreateComment(domain.Comment{
		DocID: doc.ID, AgainstVersionID: doc.CurrentVersionID,
		Anchor: domain.Anchor{Start: 0, End: 1}, AuthorIdentity: "human",
		Owner: "human", Status: domain.CommentOpen,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Fresh comment: no processing hint.
	got, _ := s.GetComment(c.ID)
	if got.ProcessingUntil != nil {
		t.Fatalf("fresh ProcessingUntil = %v, want nil", got.ProcessingUntil)
	}

	// Set → scan back the same instant (to the nanosecond, via RFC3339Nano).
	until := time.Now().UTC().Add(90 * time.Second)
	if err := s.SetProcessingUntil(c.ID, &until); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetComment(c.ID)
	if got.ProcessingUntil == nil || !got.ProcessingUntil.Equal(until) {
		t.Fatalf("ProcessingUntil = %v, want %v", got.ProcessingUntil, until)
	}
	if !got.IsProcessing(time.Now()) {
		t.Error("IsProcessing = false, want true before the deadline")
	}

	// Clear with nil.
	if err := s.SetProcessingUntil(c.ID, nil); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetComment(c.ID)
	if got.ProcessingUntil != nil {
		t.Fatalf("cleared ProcessingUntil = %v, want nil", got.ProcessingUntil)
	}
}

// TestReopenExistingDBIsIdempotent asserts a second Open over the same on-disk
// database (which already has the processing_until column from the first Open)
// does not error — the duplicate-column ALTER is ignored, so an existing db with
// the column already present never crashes on the migrate pass.
func TestReopenExistingDBIsIdempotent(t *testing.T) {
	dsn := "file:" + t.TempDir() + "/t.db"
	s1, err := Open(dsn)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	s1.Close()
	s2, err := Open(dsn)
	if err != nil {
		t.Fatalf("second Open (already migrated): %v", err)
	}
	s2.Close()
}
