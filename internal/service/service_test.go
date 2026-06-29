package service

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/rajanrx/outbox-md/internal/config"
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
	// The local human may not resolve a comment owned by an agent.
	agentC, _ := svc.PostComment(doc.ID, domain.Anchor{Start: 6, End: 11}, "agent")
	if err := svc.Resolve(agentC.ID); err == nil {
		t.Fatal("expected resolve of an agent-owned comment to fail")
	}
	// The local human resolves their own comment (identity is server-set).
	if err := svc.Resolve(c.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetComment(c.ID)
	if got.Status != domain.CommentResolved {
		t.Fatalf("status = %s, want resolved", got.Status)
	}
}

func TestRejectSuggestionReopens(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := New(s, func(_, _ string) error { return nil })
	doc, _, _ := s.CreateDocument("spec.md", "Hello world", "human")
	c, _ := svc.PostComment(doc.ID, domain.Anchor{Start: 0, End: 5}, "human")
	tok, _ := svc.Claim([]string{c.ID}, "agent")
	_, _ = svc.Propose(c.ID, tok, "Howdy world", "agent")
	if err := svc.RejectSuggestion(c.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetComment(c.ID)
	if got.Status != domain.CommentOpen {
		t.Fatalf("status = %s, want open", got.Status)
	}
}

func TestApprovePinsBaseline(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := New(s, func(_, _ string) error { return nil })
	doc, _, _ := s.CreateDocument("a.md", "v1", "human")

	app, err := svc.Approve(doc.ID, "looks good")
	if err != nil {
		t.Fatal(err)
	}
	if app.ApprovedBy != "human" {
		t.Errorf("approvedBy = %q, want human", app.ApprovedBy)
	}
	got, _ := s.GetDocument(doc.ID)
	if got.Status != domain.DocApproved || got.ApprovedVersionID != doc.CurrentVersionID {
		t.Fatalf("doc = %+v, want approved at current version", got)
	}
	// Approving again is rejected — use re-approve.
	if _, err := svc.Approve(doc.ID, ""); err == nil {
		t.Error("second approve should be rejected")
	}
}

func TestReapproveRejectedWhenNothingPending(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := New(s, func(_, _ string) error { return nil })
	doc, _, _ := s.CreateDocument("a.md", "v1", "human")
	_, _ = svc.Approve(doc.ID, "")
	if _, err := svc.Reapprove(doc.ID, ""); err == nil {
		t.Error("reapprove on approved doc with no pending changes should be rejected")
	}
}

func TestAcceptOnApprovedDocAccumulatesAmendmentWithoutWritingDisk(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	var written string
	writes := 0
	svc := New(s, func(_, content string) error { written = content; writes++; return nil })

	doc, _, _ := s.CreateDocument("a.md", "baseline", "human")
	if _, err := svc.Approve(doc.ID, ""); err != nil {
		t.Fatal(err)
	}

	// A post-approval comment is flagged.
	c, err := svc.PostComment(doc.ID, domain.Anchor{Start: 0, End: 1}, "human")
	if err != nil {
		t.Fatal(err)
	}
	if !c.PostApproval {
		t.Error("comment on approved doc should be flagged post_approval")
	}

	// Agent proposes a change; accepting it must NOT write disk or move the baseline.
	tok, _ := svc.Claim([]string{c.ID}, "agent")
	if _, err := svc.Propose(c.ID, tok, "amended baseline", "agent"); err != nil {
		t.Fatal(err)
	}
	writesBefore := writes
	if _, err := svc.Accept(c.ID); err != nil {
		t.Fatal(err)
	}
	if writes != writesBefore {
		t.Errorf("accept on approved doc wrote disk %d time(s); want 0", writes-writesBefore)
	}
	got, _ := s.GetDocument(doc.ID)
	if got.Status != domain.DocAmending {
		t.Errorf("status = %q, want amending", got.Status)
	}
	if got.ApprovedVersionID == got.CurrentVersionID {
		t.Error("baseline should not have advanced on accept")
	}

	// Re-approve advances the baseline and writes the head to disk.
	if _, err := svc.Reapprove(doc.ID, ""); err != nil {
		t.Fatal(err)
	}
	if written != "amended baseline" {
		t.Errorf("on-disk after reapprove = %q, want amended baseline", written)
	}
	got, _ = s.GetDocument(doc.ID)
	if got.Status != domain.DocApproved || got.ApprovedVersionID != got.CurrentVersionID {
		t.Errorf("after reapprove doc = %+v, want approved at head", got)
	}
}

// proposeAgainstCurrent posts a comment, claims it, and proposes content against
// the document's current version, returning the comment ID.
func proposeAgainstCurrent(t *testing.T, svc *Service, docID, content string) string {
	t.Helper()
	c, err := svc.PostComment(docID, domain.Anchor{Start: 0, End: 1}, "human")
	if err != nil {
		t.Fatal(err)
	}
	tok, err := svc.Claim([]string{c.ID}, "agent")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Propose(c.ID, tok, content, "agent"); err != nil {
		t.Fatal(err)
	}
	return c.ID
}

// P1 (Governance Seam): two governed accepts racing on the same base version of
// an APPROVED doc must serialize exactly like the draft path — one wins, one gets
// a conflict/requeue — AND the baseline pointer must never advance and the file
// on disk must never change (governed accepts accumulate ahead of the baseline
// and never write disk). Looped to actually exercise both interleavings.
func TestGovernedConcurrentAcceptsSerialize(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 300; i++ {
		s, err := store.Open("file:" + filepath.Join(dir, fmt.Sprintf("ser%d.db", i)))
		if err != nil {
			t.Fatal(err)
		}
		var mu sync.Mutex
		disk := "base" // models the on-disk baseline; writeFile is the only mutator
		writes := 0
		svc := New(s, func(_, content string) error {
			mu.Lock()
			disk = content
			writes++
			mu.Unlock()
			return nil
		})

		doc, _, _ := s.CreateDocument("spec.md", "base", "human")
		if _, err := svc.Approve(doc.ID, ""); err != nil {
			t.Fatal(err)
		}
		baseline := doc.CurrentVersionID // the pinned approved version (V1)

		// Both suggestions are post-approval, proposed against the approved head.
		c1 := proposeAgainstCurrent(t, svc, doc.ID, "AAA")
		c2 := proposeAgainstCurrent(t, svc, doc.ID, "BBB")

		var wg sync.WaitGroup
		res := make([]error, 2)
		wg.Add(2)
		go func() { defer wg.Done(); _, res[0] = svc.Accept(c1) }()
		go func() { defer wg.Done(); _, res[1] = svc.Accept(c2) }()
		wg.Wait()

		ok := 0
		for _, e := range res {
			if e == nil {
				ok++
			}
		}
		if ok != 1 {
			t.Fatalf("iter %d: expected exactly one governed accept to win, got %d (errs: %v)", i, ok, res)
		}
		cur, _ := s.GetDocument(doc.ID)
		if cur.Status != domain.DocAmending {
			t.Fatalf("iter %d: status = %q, want amending", i, cur.Status)
		}
		if cur.ApprovedVersionID != baseline {
			t.Fatalf("iter %d: baseline advanced: got %s, want %s", i, cur.ApprovedVersionID, baseline)
		}
		curVer, _ := s.GetVersion(cur.CurrentVersionID)
		if curVer.Ordinal != 2 {
			t.Fatalf("iter %d: current ordinal = %d, want 2 (one amendment)", i, curVer.Ordinal)
		}
		mu.Lock()
		gotWrites, gotDisk := writes, disk
		mu.Unlock()
		if gotWrites != 0 {
			t.Fatalf("iter %d: governed accept wrote disk %d time(s); want 0", i, gotWrites)
		}
		if gotDisk != "base" {
			t.Fatalf("iter %d: disk changed to %q; baseline must stay %q", i, gotDisk, "base")
		}
		s.Close()
	}
}

// P1 (Governance Seam): a DRAFT Approve racing a DRAFT Accept must never leave
// the document approved-but-stale. The TOCTOU: Approve reads current=V1; a
// concurrent draft Accept advances V1->V2 and writes disk; Approve then pins the
// stale V1 as the approved baseline. Corrupt signature: status==approved while
// approved_version_id != current_version_id (disk shows V2 while the baseline
// claims V1). Either operation may lose with a conflict and be retried — assert
// only that the FINAL state is one of the consistent outcomes, never the torn one.
func TestApproveAcceptInterleavingNeverCorrupts(t *testing.T) {
	// The Approve-vs-draft-Accept corruption is a rare interleave: at 500 iters a
	// single `go test` run (as CI runs it — no -race, no -count) was observed to
	// pass with the bug present; ~2500 iters reliably surfaces it. We run 5000 so
	// a plain CI run deterministically goes red if either CAS guard is reverted.
	// The on-disk invariant is modeled in the writeFile closure below (not in
	// SQLite), so an in-memory store reproduces the logical race identically while
	// avoiding the per-iteration file fsync that made a file-backed loop too slow
	// for this iteration count.
	for i := 0; i < 5000; i++ {
		s, err := store.Open(":memory:")
		if err != nil {
			t.Fatal(err)
		}
		var mu sync.Mutex
		disk := "v1" // models the on-disk file; for a draft doc it equals current
		svc := New(s, func(_, content string) error {
			mu.Lock()
			disk = content
			mu.Unlock()
			return nil
		})

		doc, _, _ := s.CreateDocument("spec.md", "v1", "human") // draft, current=V1="v1"
		c := proposeAgainstCurrent(t, svc, doc.ID, "v2")        // suggestion v2 against V1

		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); _, _ = svc.Approve(doc.ID, "") }()
		go func() { defer wg.Done(); _, _ = svc.Accept(c) }()
		wg.Wait()

		final, _ := s.GetDocument(doc.ID)
		curVer, _ := s.GetVersion(final.CurrentVersionID)
		mu.Lock()
		gotDisk := disk
		mu.Unlock()

		switch final.Status {
		case domain.DocApproved:
			// Approve won and Accept lost (conflict/retry): baseline is pinned at
			// the CURRENT version and disk equals that version's content.
			if final.ApprovedVersionID != final.CurrentVersionID {
				appVer, _ := s.GetVersion(final.ApprovedVersionID)
				t.Fatalf("iter %d: approved but approved(ord %d) != current(ord %d) — STALE baseline pinned (disk=%q)",
					i, appVer.Ordinal, curVer.Ordinal, gotDisk)
			}
			if gotDisk != curVer.Content {
				t.Fatalf("iter %d: approved but disk %q != current content %q (CORRUPTION)", i, gotDisk, curVer.Content)
			}
		case domain.DocAmending:
			// Approve won, then Accept took the governed path (read the approved
			// doc): the amendment accumulates ahead of the baseline, disk untouched.
			appVer, _ := s.GetVersion(final.ApprovedVersionID)
			if appVer.Ordinal >= curVer.Ordinal {
				t.Fatalf("iter %d: amending but approved ordinal %d not behind current %d (baseline regressed?)",
					i, appVer.Ordinal, curVer.Ordinal)
			}
			if gotDisk != appVer.Content {
				t.Fatalf("iter %d: amending but disk %q != approved-baseline content %q (CORRUPTION)", i, gotDisk, appVer.Content)
			}
		case domain.DocDraft:
			// Accept won and Approve lost (conflict/retry): still a draft, disk
			// equals the new current version's content.
			if gotDisk != curVer.Content {
				t.Fatalf("iter %d: draft but disk %q != current content %q", i, gotDisk, curVer.Content)
			}
		default:
			t.Fatalf("iter %d: unexpected status %q", i, final.Status)
		}
		s.Close()
	}
}

// P1 (Governance Seam): a governed Accept racing a Reapprove must never corrupt
// the document. After both settle the state must be one of the two CONSISTENT
// outcomes, never the regressed/stranded one:
//
//	status==approved  ⇒ approved==current && disk==current-content
//	status==amending  ⇒ approved is a real prior version behind current, and
//	                     disk==approved-baseline-content
//
// Pre-fix this fails: the stale SetDocumentApproval in Accept regresses the
// baseline below a version Reapprove already pinned, leaving disk != baseline.
func TestGovernedAcceptReapproveInterleavingNeverCorrupts(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 300; i++ {
		s, err := store.Open("file:" + filepath.Join(dir, fmt.Sprintf("inter%d.db", i)))
		if err != nil {
			t.Fatal(err)
		}
		var mu sync.Mutex
		disk := "base"
		svc := New(s, func(_, content string) error {
			mu.Lock()
			disk = content
			mu.Unlock()
			return nil
		})

		doc, _, _ := s.CreateDocument("spec.md", "base", "human")
		if _, err := svc.Approve(doc.ID, ""); err != nil { // baseline V1="base", disk="base"
			t.Fatal(err)
		}
		// Accumulate one amendment so the doc is amending with current (V2="amended")
		// ahead of the baseline (V1). Disk stays "base" (governed accept writes none).
		cAmend := proposeAgainstCurrent(t, svc, doc.ID, "amended")
		if _, err := svc.Accept(cAmend); err != nil {
			t.Fatal(err)
		}
		// A fresh post-approval suggestion proposed against the current head (V2).
		cNext := proposeAgainstCurrent(t, svc, doc.ID, "amended2")

		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); _, _ = svc.Accept(cNext) }()
		go func() { defer wg.Done(); _, _ = svc.Reapprove(doc.ID, "") }()
		wg.Wait()

		final, _ := s.GetDocument(doc.ID)
		curVer, _ := s.GetVersion(final.CurrentVersionID)
		appVer, _ := s.GetVersion(final.ApprovedVersionID)
		mu.Lock()
		gotDisk := disk
		mu.Unlock()

		switch final.Status {
		case domain.DocApproved:
			if final.ApprovedVersionID != final.CurrentVersionID {
				t.Fatalf("iter %d: approved but current(%d)!=approved(%d) — version stranded as approved",
					i, curVer.Ordinal, appVer.Ordinal)
			}
			if gotDisk != curVer.Content {
				t.Fatalf("iter %d: approved but disk %q != current content %q", i, gotDisk, curVer.Content)
			}
		case domain.DocAmending:
			if appVer.Ordinal >= curVer.Ordinal {
				t.Fatalf("iter %d: amending but approved ordinal %d not behind current %d (baseline regressed?)",
					i, appVer.Ordinal, curVer.Ordinal)
			}
			if gotDisk != appVer.Content {
				t.Fatalf("iter %d: amending but disk %q != approved-baseline content %q (CORRUPTION)",
					i, gotDisk, appVer.Content)
			}
		default:
			t.Fatalf("iter %d: unexpected status %q", i, final.Status)
		}
		s.Close()
	}
}

func TestClaimRejectsOverBatchSize(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := New(s, func(_, _ string) error { return nil })
	svc.SetConfig(config.Config{Agent: config.AgentConfig{BatchSize: 2}, Approval: config.ApprovalConfig{PostApprovalComments: true}})

	doc, _, _ := s.CreateDocument("a.md", "hello world", "human")
	c1, _ := svc.PostComment(doc.ID, domain.Anchor{Start: 0, End: 1}, "human")
	c2, _ := svc.PostComment(doc.ID, domain.Anchor{Start: 1, End: 2}, "human")
	c3, _ := svc.PostComment(doc.ID, domain.Anchor{Start: 2, End: 3}, "human")

	if _, err := svc.Claim([]string{c1.ID, c2.ID, c3.ID}, "agent"); err == nil {
		t.Fatal("claiming 3 with batch_size 2 should be rejected")
	}
	if got, _ := s.GetComment(c1.ID); got.Status != domain.CommentOpen {
		t.Errorf("c1 status = %s, want open (over-batch claim must claim nothing)", got.Status)
	}
	if _, err := svc.Claim([]string{c1.ID, c2.ID}, "agent"); err != nil {
		t.Fatalf("within-cap claim failed: %v", err)
	}
}

func TestPostCommentBlockedOnApprovedWhenDisabled(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := New(s, func(_, _ string) error { return nil })
	svc.SetConfig(config.Config{Agent: config.AgentConfig{BatchSize: 5}, Approval: config.ApprovalConfig{PostApprovalComments: false}})

	doc, _, _ := s.CreateDocument("a.md", "hello", "human")
	if _, err := svc.Approve(doc.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PostComment(doc.ID, domain.Anchor{Start: 0, End: 1}, "human"); err == nil {
		t.Fatal("post-approval comment should be rejected when disabled")
	}
	svc.SetConfig(config.Config{Agent: config.AgentConfig{BatchSize: 5}, Approval: config.ApprovalConfig{PostApprovalComments: true}})
	if _, err := svc.PostComment(doc.ID, domain.Anchor{Start: 0, End: 1}, "human"); err != nil {
		t.Fatalf("post-approval comment should be allowed when enabled: %v", err)
	}
}
