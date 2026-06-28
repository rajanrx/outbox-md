package service

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/rajanrx/outbox-md/internal/domain"
	"github.com/rajanrx/outbox-md/internal/store"
)

// P1: two accepts racing on the same base version must serialize — exactly one
// wins, and the on-disk file equals the winning version (no clobber).
func TestConcurrentAcceptsSerialize(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open("file:" + filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var mu sync.Mutex
	written := ""
	svc := New(s, func(_, content string) error {
		mu.Lock()
		written = content
		mu.Unlock()
		return nil
	})
	doc, _, _ := s.CreateDocument("spec.md", "base", "human")
	c1, _ := svc.PostComment(doc.ID, domain.Anchor{Start: 0, End: 4}, "human")
	c2, _ := svc.PostComment(doc.ID, domain.Anchor{Start: 0, End: 4}, "human")
	t1, _ := svc.Claim([]string{c1.ID}, "agent")
	_, _ = svc.Propose(c1.ID, t1, "AAA", "agent") // both against the base version
	t2, _ := svc.Claim([]string{c2.ID}, "agent")
	_, _ = svc.Propose(c2.ID, t2, "BBB", "agent")

	var wg sync.WaitGroup
	res := make([]error, 2)
	wg.Add(2)
	go func() { defer wg.Done(); _, res[0] = svc.Accept(c1.ID) }()
	go func() { defer wg.Done(); _, res[1] = svc.Accept(c2.ID) }()
	wg.Wait()

	ok := 0
	for _, e := range res {
		if e == nil {
			ok++
		}
	}
	if ok != 1 {
		t.Fatalf("expected exactly one accept to win, got %d (errs: %v)", ok, res)
	}
	cur, _ := s.GetDocument(doc.ID)
	curVer, _ := s.GetVersion(cur.CurrentVersionID)
	if written != curVer.Content {
		t.Fatalf("on-disk %q != current version %q", written, curVer.Content)
	}
	if curVer.Ordinal != 2 {
		t.Fatalf("ordinal = %d, want 2 (only one new version)", curVer.Ordinal)
	}
}

func TestAcceptRewritesFileAndReanchors(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	var written string
	svc := New(s, func(_, content string) error { written = content; return nil })

	doc, _, _ := s.CreateDocument("spec.md", "Hello world", "human")
	cWorld, _ := svc.PostComment(doc.ID, domain.Anchor{Start: 6, End: 11}, "human") // "world"
	cHello, _ := svc.PostComment(doc.ID, domain.Anchor{Start: 0, End: 5}, "human")  // "Hello"

	tok, _ := svc.Claim([]string{cHello.ID}, "agent")
	if _, err := svc.Propose(cHello.ID, tok, "Say Hello world", "agent"); err != nil {
		t.Fatal(err)
	}
	nv, err := svc.Accept(cHello.ID)
	if err != nil {
		t.Fatal(err)
	}

	if nv.Content != "Say Hello world" || written != "Say Hello world" {
		t.Fatalf("content=%q written=%q", nv.Content, written)
	}
	// The OTHER comment must follow its text from [6,11) to [10,15).
	gotWorld, _ := s.GetComment(cWorld.ID)
	if gotWorld.Anchor != (domain.Anchor{Start: 10, End: 15}) {
		t.Fatalf("world anchor = %+v, want {10,15}", gotWorld.Anchor)
	}
	if gotWorld.Status != domain.CommentOpen {
		t.Fatalf("world status = %s, want open", gotWorld.Status)
	}
	gotHello, _ := s.GetComment(cHello.ID)
	if gotHello.Status != domain.CommentResolved {
		t.Fatalf("hello status = %s, want resolved", gotHello.Status)
	}
}

// P1: a suggestion proposed against an older version must not clobber a newer
// accepted edit.
func TestAcceptRejectsStaleSuggestion(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	var written string
	svc := New(s, func(_, content string) error { written = content; return nil })
	doc, _, _ := s.CreateDocument("spec.md", "Hello world", "human")

	c1, _ := svc.PostComment(doc.ID, domain.Anchor{Start: 0, End: 5}, "human")
	c2, _ := svc.PostComment(doc.ID, domain.Anchor{Start: 6, End: 11}, "human")
	t1, _ := svc.Claim([]string{c1.ID}, "agent")
	_, _ = svc.Propose(c1.ID, t1, "AAA", "agent") // against v1
	t2, _ := svc.Claim([]string{c2.ID}, "agent")
	_, _ = svc.Propose(c2.ID, t2, "BBB", "agent") // against v1

	if _, err := svc.Accept(c1.ID); err != nil {
		t.Fatal(err)
	}
	if written != "AAA" {
		t.Fatalf("after first accept written=%q", written)
	}
	// Accepting c2 (still against v1) must be rejected, not overwrite "AAA".
	if _, err := svc.Accept(c2.ID); err == nil {
		t.Fatal("expected stale rejection")
	}
	if written != "AAA" {
		t.Fatalf("stale accept clobbered the file: %q", written)
	}
	cur, _ := s.GetDocument(doc.ID)
	curVer, _ := s.GetVersion(cur.CurrentVersionID)
	if curVer.Content != "AAA" {
		t.Fatalf("current content=%q, want AAA", curVer.Content)
	}
	got2, _ := s.GetComment(c2.ID)
	if got2.Status != domain.CommentOpen {
		t.Fatalf("stale comment status=%s, want open (re-queued)", got2.Status)
	}
}

// P2: two concurrent accepts of the SAME comment must not leave the lifecycle
// inconsistent — the loser's requeue must never undo the winner's accept.
func TestDuplicateAcceptSameCommentStaysConsistent(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open("file:" + filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	svc := New(s, func(_, _ string) error { return nil })
	doc, _, _ := s.CreateDocument("spec.md", "base", "human")
	c, _ := svc.PostComment(doc.ID, domain.Anchor{Start: 0, End: 4}, "human")
	tok, _ := svc.Claim([]string{c.ID}, "agent")
	_, _ = svc.Propose(c.ID, tok, "AAA", "agent")

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = svc.Accept(c.ID) }()
	go func() { defer wg.Done(); _, _ = svc.Accept(c.ID) }()
	wg.Wait()

	gotC, _ := s.GetComment(c.ID)
	if gotC.Status != domain.CommentResolved {
		t.Fatalf("comment status = %s, want resolved (loser must not re-open it)", gotC.Status)
	}
	sg, _, _ := s.GetSuggestionByComment(c.ID)
	if sg.State != domain.SuggestionAccepted {
		t.Fatalf("suggestion state = %s, want accepted (loser must not reject it)", sg.State)
	}
	cur, _ := s.GetDocument(doc.ID)
	curVer, _ := s.GetVersion(cur.CurrentVersionID)
	if curVer.Ordinal != 2 {
		t.Fatalf("ordinal = %d, want 2 (exactly one new version)", curVer.Ordinal)
	}
}

// P2: an already-accepted comment/suggestion cannot be accepted again.
func TestAcceptRejectsRepeatedAccept(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := New(s, func(_, _ string) error { return nil })
	doc, _, _ := s.CreateDocument("spec.md", "Hello world", "human")
	c, _ := svc.PostComment(doc.ID, domain.Anchor{Start: 0, End: 5}, "human")
	tok, _ := svc.Claim([]string{c.ID}, "agent")
	_, _ = svc.Propose(c.ID, tok, "Howdy world", "agent")
	if _, err := svc.Accept(c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Accept(c.ID); err == nil {
		t.Fatal("expected error on repeated accept")
	}
	cur, _ := s.GetDocument(doc.ID)
	curVer, _ := s.GetVersion(cur.CurrentVersionID)
	if curVer.Ordinal != 2 {
		t.Fatalf("ordinal=%d, want 2 (no duplicate version)", curVer.Ordinal)
	}
}

// P1: a failed file write must not advance the database current pointer.
func TestAcceptFailedWriteDoesNotAdvanceDB(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := New(s, func(_, _ string) error { return errors.New("disk full") })
	doc, v1, _ := s.CreateDocument("spec.md", "Hello world", "human")
	c, _ := svc.PostComment(doc.ID, domain.Anchor{Start: 0, End: 5}, "human")
	tok, _ := svc.Claim([]string{c.ID}, "agent")
	_, _ = svc.Propose(c.ID, tok, "Howdy world", "agent")
	if _, err := svc.Accept(c.ID); err == nil {
		t.Fatal("expected write error")
	}
	cur, _ := s.GetDocument(doc.ID)
	if cur.CurrentVersionID != v1.ID {
		t.Fatal("current version advanced despite failed write")
	}
}

func TestProposeRejectsBadToken(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := New(s, func(_, _ string) error { return nil })
	doc, _, _ := s.CreateDocument("spec.md", "Hello world", "human")
	c, _ := svc.PostComment(doc.ID, domain.Anchor{Start: 0, End: 5}, "human")
	if _, err := svc.Propose(c.ID, "wrong-token", "x", "agent"); err == nil {
		t.Fatal("expected error for invalid claim token")
	}
}

func TestHumanReplyAndResolve(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := New(s, func(_, _ string) error { return nil })
	doc, _, _ := s.CreateDocument("spec.md", "Hello world", "human")
	c, _ := svc.PostComment(doc.ID, domain.Anchor{Start: 0, End: 5}, "human")

	if _, err := svc.HumanReply(c.ID, "what about X?"); err != nil {
		t.Fatal(err)
	}
	thread, _ := s.ListThread(c.ID)
	if len(thread) != 1 || thread[0].Body != "what about X?" {
		t.Fatalf("thread = %+v", thread)
	}
	if err := svc.Resolve(c.ID, "someone-else"); err == nil {
		t.Fatal("expected non-owner resolve to fail")
	}
	if err := svc.Resolve(c.ID, "human"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetComment(c.ID)
	if got.Status != domain.CommentResolved {
		t.Fatalf("status = %s, want resolved", got.Status)
	}
}
