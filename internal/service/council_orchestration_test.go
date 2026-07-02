package service

import (
	"testing"

	"github.com/rajanrx/outbox-md/internal/config"
	"github.com/rajanrx/outbox-md/internal/domain"
)

// TestCouncilQueueAppliesSourcesWhitelist (PR #74 P1): the council queue must
// apply the SAME sources whitelist as the read/write paths. A comment on a doc
// hidden by narrowed sources must NOT surface to OpenCouncilComments (else the
// council would claim/heartbeat/review it) nor count in PendingCommentCount (else
// it would keep triggering runs). The served comment comes back WITH review
// context (DocID + excerpt).
func TestCouncilQueueAppliesSourcesWhitelist(t *testing.T) {
	svc, s := newSvc(t)
	shown, _, _ := s.CreateDocument("shown/a.md", "hello world", "human")
	hidden, _, _ := s.CreateDocument("hidden/b.md", "secret text", "human")
	cShown, _ := svc.PostComment(shown.ID, domain.Anchor{Start: 0, End: 5}, "human")
	if _, err := svc.PostComment(hidden.ID, domain.Anchor{Start: 0, End: 6}, "human"); err != nil {
		t.Fatal(err)
	}

	// Narrow sources to "shown" only → "hidden/b.md" is not served.
	svc.SetConfig(config.Config{Sources: []string{"shown"}})

	got, err := svc.OpenCouncilComments("")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].CommentID != cShown.ID {
		t.Fatalf("OpenCouncilComments = %+v, want only the shown-doc comment", got)
	}
	// Review context is populated so the member can read_doc + see the excerpt.
	if got[0].DocID != shown.ID || got[0].Excerpt == "" {
		t.Fatalf("council comment missing DocID/Excerpt: %+v", got[0])
	}

	if n, err := svc.PendingCommentCount(""); err != nil || n != 1 {
		t.Fatalf("PendingCommentCount = %d err=%v, want 1 (hidden excluded)", n, err)
	}
}
