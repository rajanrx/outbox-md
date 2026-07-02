package service

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/rajanrx/outbox-md/internal/domain"
	"github.com/rajanrx/outbox-md/internal/store"
)

// newSvc builds a service over a fresh on-disk store with a no-op writer.
func newSvc(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open("file:" + filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return New(s, func(_, _, _ string) error { return nil }), s
}

// TestClaimReturnsOnlyWonIDs: Claim over a batch returns only the ids actually
// won. An id already held by a live (fresh, non-stale) claim is skipped — not
// errored — so the batch's other ids still claim and the agent processes only
// what it holds.
func TestClaimReturnsOnlyWonIDs(t *testing.T) {
	svc, s := newSvc(t)
	doc, _, _ := s.CreateDocument("spec.md", "hello world", "human")
	c1, _ := svc.PostComment(doc.ID, domain.Anchor{Start: 0, End: 5}, "human")
	c2, _ := svc.PostComment(doc.ID, domain.Anchor{Start: 6, End: 11}, "human")

	// A first agent claims c1 (a live, fresh claim).
	if _, won, err := svc.Claim([]string{c1.ID}, "agent-a"); err != nil || len(won) != 1 {
		t.Fatalf("first claim won=%v err=%v", won, err)
	}
	// A second agent tries the whole batch; c1 is live-claimed so it is skipped,
	// c2 is open so it is won. Only c2 comes back.
	tok, won, err := svc.Claim([]string{c1.ID, c2.ID}, "agent-b")
	if err != nil {
		t.Fatal(err)
	}
	if tok == "" {
		t.Fatal("expected a claim token")
	}
	if len(won) != 1 || won[0] != c2.ID {
		t.Fatalf("won = %v, want exactly [%s]", won, c2.ID)
	}
}

// TestProposeAgainstCurrentVersionSucceeds: the happy path — an agent claims and
// proposes against the document's current version, so the pre-post staleness
// gate lets it through.
func TestProposeAgainstCurrentVersionSucceeds(t *testing.T) {
	svc, s := newSvc(t)
	doc, _, _ := s.CreateDocument("spec.md", "hello world", "human")
	c, _ := svc.PostComment(doc.ID, domain.Anchor{Start: 0, End: 5}, "human")

	tok, won, err := svc.Claim([]string{c.ID}, "agent")
	if err != nil || len(won) != 1 {
		t.Fatalf("claim won=%v err=%v", won, err)
	}
	if _, err := svc.Propose(c.ID, tok, "howdy world", "agent"); err != nil {
		t.Fatalf("propose against current version failed: %v", err)
	}
}

// TestProposeAgainstSupersededVersionRejected: the staleness safety — after the
// document advances (a watcher-imported on-disk edit adds a version WITHOUT
// rebasing the comment), a propose against the version the agent read is rejected
// with a clear, versioned error. This gets more likely under fan-out + live
// editing; we must never silently store a suggestion built on stale content.
func TestProposeAgainstSupersededVersionRejected(t *testing.T) {
	svc, s := newSvc(t)
	doc, _, _ := s.CreateDocument("spec.md", "hello world", "human")
	c, _ := svc.PostComment(doc.ID, domain.Anchor{Start: 0, End: 5}, "human")

	tok, won, err := svc.Claim([]string{c.ID}, "agent")
	if err != nil || len(won) != 1 {
		t.Fatalf("claim won=%v err=%v", won, err)
	}

	// The document changes under the agent (v1 -> v2), like a live on-disk edit
	// the watcher re-imports. The comment still points at v1.
	if _, err := s.AddVersion(doc.ID, "hello brave new world", "watch"); err != nil {
		t.Fatal(err)
	}

	_, err = svc.Propose(c.ID, tok, "howdy world", "agent")
	if err == nil {
		t.Fatal("propose against superseded version succeeded, want stale rejection")
	}
	msg := err.Error()
	if !strings.Contains(msg, "document changed") || !strings.Contains(msg, "re-propose") {
		t.Fatalf("error %q missing the versioned re-read guidance", msg)
	}
	if !strings.Contains(msg, "v1") || !strings.Contains(msg, "v2") {
		t.Fatalf("error %q should name the version ordinals (v1->v2)", msg)
	}
	// The stale propose stored nothing.
	if _, ok, _ := s.GetSuggestionByComment(c.ID); ok {
		t.Fatal("a suggestion was stored for a stale propose; must be rejected before storage")
	}
}
