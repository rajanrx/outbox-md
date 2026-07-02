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
	open, _ := s.ListOpenComments(time.Now())
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

func hasComment(cs []domain.Comment, id string) bool {
	for _, c := range cs {
		if c.ID == id {
			return true
		}
	}
	return false
}

// TestListOpenCommentsRecoversStaleClaims is the core stale-claim recovery:
// list_open_comments must return 'open' comments AND any 'claimed' comment whose
// claim has been abandoned (no live heartbeat, older than the grace), while
// keeping a live-heartbeated claim out (so it is never double-worked) and every
// terminal/other status excluded. This is what un-strands a burst the agent only
// partly finished.
func TestListOpenCommentsRecoversStaleClaims(t *testing.T) {
	s, _ := Open(":memory:")
	defer s.Close()
	doc, v1, _ := s.CreateDocument("spec.md", "hello world", "human")

	mk := func(status domain.CommentStatus) domain.Comment {
		c, err := s.CreateComment(domain.Comment{
			DocID: doc.ID, AgainstVersionID: v1.ID,
			Anchor:         domain.Anchor{Start: 0, End: 1},
			AuthorIdentity: "human", Owner: "human", Status: domain.CommentOpen,
		})
		if err != nil {
			t.Fatal(err)
		}
		if status != domain.CommentOpen {
			if err := s.UpdateCommentStatus(c.ID, status, "tok"); err != nil {
				t.Fatal(err)
			}
		}
		return c
	}

	openC := mk(domain.CommentOpen)
	staleClaim := mk(domain.CommentClaimed)   // no heartbeat set → abandoned
	nullClaim := mk(domain.CommentClaimed)    // processing_until stays NULL → abandoned
	liveClaim := mk(domain.CommentClaimed)    // future heartbeat → actively worked
	expiredClaim := mk(domain.CommentClaimed) // past heartbeat → abandoned
	resolved := mk(domain.CommentResolved)
	replied := mk(domain.CommentReplied)
	addressed := mk(domain.CommentAddressed)

	// A live heartbeat sits well beyond the evaluation `now`.
	future := time.Now().Add(StaleClaimGrace + 10*time.Minute)
	if err := s.SetProcessingUntil(liveClaim.ID, &future); err != nil {
		t.Fatal(err)
	}
	// An expired heartbeat is in the past.
	past := time.Now().Add(-time.Hour)
	if err := s.SetProcessingUntil(expiredClaim.ID, &past); err != nil {
		t.Fatal(err)
	}

	// Evaluate the work set at a `now` past the grace, so the claims (stamped at
	// ~real-now) read as aged. `future` is still ahead of this now (live).
	now := time.Now().Add(StaleClaimGrace + time.Minute)
	got, err := s.ListOpenComments(now)
	if err != nil {
		t.Fatal(err)
	}

	if !hasComment(got, openC.ID) {
		t.Error("open comment must be in the work set")
	}
	for _, c := range []domain.Comment{staleClaim, nullClaim, expiredClaim} {
		if !hasComment(got, c.ID) {
			t.Errorf("abandoned claim %s must be recovered into the work set", c.ID)
		}
	}
	if hasComment(got, liveClaim.ID) {
		t.Error("a live-heartbeated claim must NOT be returned (no double-work)")
	}
	for _, c := range []domain.Comment{resolved, replied, addressed} {
		if hasComment(got, c.ID) {
			t.Errorf("status %s must stay excluded from the work set", c.Status)
		}
	}
}

// TestListOpenCommentsFreshClaimNotResurfaced is the claim→mark_processing race
// guard: a claim just made (within the grace, before the agent's first
// mark_processing) must NOT be resurfaced, or a concurrent list would hand the
// same comment to a second worker. Once aged with an expired hint it is recovered.
func TestListOpenCommentsFreshClaimNotResurfaced(t *testing.T) {
	s, _ := Open(":memory:")
	defer s.Close()
	doc, v1, _ := s.CreateDocument("spec.md", "hi there", "human")
	c, err := s.CreateComment(domain.Comment{
		DocID: doc.ID, AgainstVersionID: v1.ID,
		Anchor:         domain.Anchor{Start: 0, End: 1},
		AuthorIdentity: "human", Owner: "human", Status: domain.CommentOpen,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateCommentStatus(c.ID, domain.CommentClaimed, "tok"); err != nil {
		t.Fatal(err)
	}

	// Right now the claim is inside the grace window → not yet our work.
	if got, _ := s.ListOpenComments(time.Now()); hasComment(got, c.ID) {
		t.Fatal("a claim just made (within grace) must not be resurfaced")
	}

	// Aged past the grace with an expired hint → recovered.
	now := time.Now().Add(StaleClaimGrace + time.Minute)
	if got, _ := s.ListOpenComments(now); !hasComment(got, c.ID) {
		t.Fatal("an aged, un-heart-beated claim must be recovered")
	}
}

// TestRequeueClaimedCommentsForProject: the `outbox retry` primitive resets a
// project's claimed comments to open, clearing the claim fields, and reports the
// count. Comments in OTHER projects and non-claimed comments are untouched.
func TestRequeueClaimedCommentsForProject(t *testing.T) {
	s, _ := Open(":memory:")
	defer s.Close()

	docA, _, _ := s.CreateDocumentInProject("alpha", "a.md", "hello", "human")
	docB, _, _ := s.CreateDocumentInProject("beta", "b.md", "hello", "human")

	// Two claimed + one open in alpha; one claimed in beta.
	c1, _ := s.CreateComment(domain.Comment{DocID: docA.ID, Status: domain.CommentOpen, AuthorIdentity: "h", Owner: "h"})
	c2, _ := s.CreateComment(domain.Comment{DocID: docA.ID, Status: domain.CommentOpen, AuthorIdentity: "h", Owner: "h"})
	cOpen, _ := s.CreateComment(domain.Comment{DocID: docA.ID, Status: domain.CommentOpen, AuthorIdentity: "h", Owner: "h"})
	cBeta, _ := s.CreateComment(domain.Comment{DocID: docB.ID, Status: domain.CommentOpen, AuthorIdentity: "h", Owner: "h"})

	// Claim c1, c2 (alpha) and cBeta (beta) — stamps claim_token + claimed_at, and
	// give c1 a processing hint so we can confirm it is cleared.
	if err := s.UpdateCommentStatus(c1.ID, domain.CommentClaimed, "tok-a"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateCommentStatus(c2.ID, domain.CommentClaimed, "tok-a"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateCommentStatus(cBeta.ID, domain.CommentClaimed, "tok-b"); err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(time.Minute)
	if err := s.SetProcessingUntil(c1.ID, &until); err != nil {
		t.Fatal(err)
	}

	n, err := s.RequeueClaimedCommentsForProject("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("re-queued %d in alpha, want 2", n)
	}

	// c1, c2 are back to open with the claim fully cleared.
	for _, id := range []string{c1.ID, c2.ID} {
		got, err := s.GetComment(id)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != domain.CommentOpen {
			t.Fatalf("comment %s status = %q, want open", id, got.Status)
		}
		if got.ClaimToken != "" || got.ProcessingUntil != nil || got.ClaimedAt != nil {
			t.Fatalf("comment %s claim not cleared: token=%q processing=%v claimedAt=%v",
				id, got.ClaimToken, got.ProcessingUntil, got.ClaimedAt)
		}
	}

	// The already-open comment is unaffected; beta's claimed comment is untouched.
	if got, _ := s.GetComment(cOpen.ID); got.Status != domain.CommentOpen {
		t.Fatalf("pre-open comment status = %q, want open", got.Status)
	}
	if got, _ := s.GetComment(cBeta.ID); got.Status != domain.CommentClaimed || got.ClaimToken != "tok-b" {
		t.Fatalf("beta comment = %q token=%q, want claimed tok-b (untouched)", got.Status, got.ClaimToken)
	}

	// Re-queuing a project with no claimed comments reports 0.
	if n, err := s.RequeueClaimedCommentsForProject("alpha"); err != nil || n != 0 {
		t.Fatalf("second retry = %d, %v; want 0, nil", n, err)
	}
}
