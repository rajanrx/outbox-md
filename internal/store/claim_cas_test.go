package store

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rajanrx/outbox-md/internal/domain"
)

// mkOpenComment creates a doc + one open comment and returns its id.
func mkOpenComment(t *testing.T, s *Store) string {
	t.Helper()
	doc, v1, err := s.CreateDocument("spec.md", "hello world", "human")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateComment(domain.Comment{
		DocID: doc.ID, AgainstVersionID: v1.ID,
		Anchor:         domain.Anchor{Start: 0, End: 5},
		AuthorIdentity: "human", Owner: "human", Status: domain.CommentOpen,
	})
	if err != nil {
		t.Fatal(err)
	}
	return c.ID
}

// TestClaimCommentCAS_ConcurrentSingleWinner is the fan-out safety invariant: with
// N agents racing, two must NEVER both claim the same comment. Many goroutines
// CAS-claim one open comment at once; exactly one wins, and the stored token is
// the winner's.
func TestClaimCommentCAS_ConcurrentSingleWinner(t *testing.T) {
	s, _ := Open(":memory:")
	defer s.Close()
	id := mkOpenComment(t, s)

	const racers = 16
	now := time.Now().UTC()
	var wins int32
	var winnerTok atomic.Value
	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	done.Add(racers)
	for i := 0; i < racers; i++ {
		tok := domain.NewID()
		go func() {
			defer done.Done()
			start.Wait() // release all at once to maximise the race
			won, err := s.ClaimCommentCAS(id, tok, now, StaleClaimGrace)
			if err != nil {
				t.Errorf("CAS error: %v", err)
				return
			}
			if won {
				atomic.AddInt32(&wins, 1)
				winnerTok.Store(tok)
			}
		}()
	}
	start.Done()
	done.Wait()

	if got := atomic.LoadInt32(&wins); got != 1 {
		t.Fatalf("winners = %d, want exactly 1 (double-claim!)", got)
	}
	c, err := s.GetComment(id)
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != domain.CommentClaimed {
		t.Fatalf("status = %q, want claimed", c.Status)
	}
	if c.ClaimToken != winnerTok.Load().(string) {
		t.Fatalf("stored token %q != winner token %q", c.ClaimToken, winnerTok.Load())
	}
}

// TestClaimCommentCAS_OpenClaimableOnce: a straight (uncontended) claim of an open
// comment wins once; a second claim of the now-claimed (fresh, not stale) comment
// loses — the CAS refuses to re-claim a live claim.
func TestClaimCommentCAS_OpenClaimableOnce(t *testing.T) {
	s, _ := Open(":memory:")
	defer s.Close()
	id := mkOpenComment(t, s)
	now := time.Now().UTC()

	won, err := s.ClaimCommentCAS(id, "tok-1", now, StaleClaimGrace)
	if err != nil || !won {
		t.Fatalf("first claim won=%v err=%v, want won=true", won, err)
	}
	// Fresh claim (claimed_at ~= now) is NOT stale → not re-claimable.
	won, err = s.ClaimCommentCAS(id, "tok-2", now, StaleClaimGrace)
	if err != nil {
		t.Fatal(err)
	}
	if won {
		t.Fatal("second claim won, want lost (live claim must not be re-claimable)")
	}
}

// TestClaimCommentCAS_StaleReclaimable: an abandoned (stale) claim IS re-claimable.
// We claim, then evaluate staleness at a time well past the grace window, so the
// claim reads as abandoned and a fresh agent wins it back.
func TestClaimCommentCAS_StaleReclaimable(t *testing.T) {
	s, _ := Open(":memory:")
	defer s.Close()
	id := mkOpenComment(t, s)
	base := time.Now().UTC()

	if won, err := s.ClaimCommentCAS(id, "old-agent", base, StaleClaimGrace); err != nil || !won {
		t.Fatalf("initial claim won=%v err=%v", won, err)
	}
	// Evaluate far in the future so the (real-now) claimed_at is older than grace.
	future := base.Add(StaleClaimGrace + time.Hour)
	won, err := s.ClaimCommentCAS(id, "new-agent", future, StaleClaimGrace)
	if err != nil {
		t.Fatal(err)
	}
	if !won {
		t.Fatal("stale claim not re-claimable, want won=true")
	}
	c, err := s.GetComment(id)
	if err != nil {
		t.Fatal(err)
	}
	if c.ClaimToken != "new-agent" {
		t.Fatalf("token = %q, want new-agent (reclaim did not swap the token)", c.ClaimToken)
	}
}
