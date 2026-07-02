package autoreply

import (
	"bytes"
	"context"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rajanrx/outbox-md/internal/domain"
	"github.com/rajanrx/outbox-md/internal/webhook"
)

// discRecorder captures every council spawn a discussion run makes. Member commands
// start "member-cmd"; the chair is "chair-cmd". It records each member prompt (so a
// test can inspect the injected transcript and round) and, at chair entry, how many
// members had RETURNED — the signal that the chair runs only after the whole
// (multi-round) member fan-out has joined.
type discRecorder struct {
	mu              sync.Mutex
	memberStarts    int
	memberDone      int
	memberPrompts   []string
	chairStarts     int
	chairSawMembers int
	block           time.Duration
}

func (r *discRecorder) spawn(_ context.Context, _, agentCmd, prompt string) error {
	if strings.HasPrefix(agentCmd, "member-cmd") {
		r.mu.Lock()
		r.memberStarts++
		r.memberPrompts = append(r.memberPrompts, prompt)
		block := r.block
		r.mu.Unlock()
		if block > 0 {
			time.Sleep(block)
		}
		r.mu.Lock()
		r.memberDone++
		r.mu.Unlock()
		return nil
	}
	r.mu.Lock()
	r.chairStarts++
	r.chairSawMembers = r.memberDone
	r.mu.Unlock()
	return nil
}

// discConfig builds a council engine Config with the discussion seam (CouncilState)
// wired — the only thing that turns the discussion loop on. The comment is served
// once (a claimed comment leaves the open set in production); state is the caller's
// transcript fake; rounds/budget configure the loop.
func discConfig(rec *discRecorder, members []string, chair string, rounds, budget int, state func(string) (CouncilSnapshot, error)) Config {
	var served int32
	return Config{
		Enabled:     true,
		Debounce:    testDebounce,
		Concurrency: len(members),
		Resolve:     func(any) string { return "p" },
		Targets: map[string]Target{
			"p": {Root: "/p", Members: members, Chair: chair},
		},
		Spawn: rec.spawn,
		Claim: func(string) (string, bool, error) { return "tok", true, nil },
		OpenComments: func(string) ([]CommentRef, error) {
			if atomic.AddInt32(&served, 1) > 1 {
				return nil, nil
			}
			return []CommentRef{{ID: "c1", DocID: "doc-1", Excerpt: "flagged sentence", Thread: "human: fix this"}}, nil
		},
		Heartbeat:                func(string, string) error { return nil },
		CouncilState:             state,
		CouncilRounds:            rounds,
		CouncilBudget:            budget,
		CouncilDeadlockThreshold: 50,
	}
}

// flipState returns a CouncilState fake whose member-1 verdict flips on every call,
// so councilPositionsChanged always reports a change — the loop is then bounded only
// by rounds/budget, never by early-exit. member-2 is stable.
func flipState() func(string) (CouncilSnapshot, error) {
	var calls int32
	return func(string) (CouncilSnapshot, error) {
		n := atomic.AddInt32(&calls, 1)
		v := domain.VerdictReply
		if n%2 == 0 {
			v = domain.VerdictRejectComment
		}
		return CouncilSnapshot{Candidates: []CouncilTurn{
			{AgentIdentity: "member-1", Round: int(n), Lens: domain.LensCorrectness, Verdict: v, Rationale: "m1"},
			{AgentIdentity: "member-2", Round: int(n), Lens: domain.LensSkeptic, Verdict: domain.VerdictReply, Rationale: "m2"},
		}}, nil
	}
}

// TestCouncilRunsDiscussionRoundsWhenPositionsChange: a 2-member council with
// rounds=2 and a CouncilState that reports a CHANGED position after every round runs
// round 0 + 2 discussion rounds (members re-spawned each round), THEN the chair —
// and the chair runs only after the whole multi-round fan-out has joined.
func TestCouncilRunsDiscussionRoundsWhenPositionsChange(t *testing.T) {
	rec := &discRecorder{}
	e := New(discConfig(rec, []string{"member-cmd-a", "member-cmd-b"}, "chair-cmd", 2, 0, flipState()))
	defer e.Close()

	e.Fire(webhook.EventCommentCreated, nil)
	waitFor(t, func() bool {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		return rec.chairStarts == 1
	})

	rec.mu.Lock()
	defer rec.mu.Unlock()
	// round 0 + 2 discussion rounds = 3 waves × 2 members = 6 member spawns.
	if rec.memberStarts != 6 {
		t.Fatalf("member spawns = %d, want 6 (round 0 + 2 discussion rounds × 2 members)", rec.memberStarts)
	}
	if rec.chairStarts != 1 {
		t.Fatalf("chair spawns = %d, want 1", rec.chairStarts)
	}
	if rec.chairSawMembers != 6 {
		t.Fatalf("chair started with %d members returned, want 6 (chair runs after the whole multi-round join)", rec.chairSawMembers)
	}
}

// TestCouncilEarlyExitWhenStable: a CouncilState reporting NO change after round 1
// stops before round 2 (mechanical consensus early-exit), and the chair runs on the
// round-0/round-1 result. So a stable council pays exactly one discussion round.
func TestCouncilEarlyExitWhenStable(t *testing.T) {
	rec := &discRecorder{}
	stable := CouncilSnapshot{Candidates: []CouncilTurn{
		{AgentIdentity: "member-1", Round: 0, Lens: domain.LensCorrectness, Verdict: domain.VerdictReply, Rationale: "m1"},
		{AgentIdentity: "member-2", Round: 0, Lens: domain.LensSkeptic, Verdict: domain.VerdictReply, Rationale: "m2"},
	}}
	state := func(string) (CouncilSnapshot, error) { return stable, nil } // never changes
	e := New(discConfig(rec, []string{"member-cmd-a", "member-cmd-b"}, "chair-cmd", 2, 0, state))
	defer e.Close()

	e.Fire(webhook.EventCommentCreated, nil)
	waitFor(t, func() bool {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		return rec.chairStarts == 1
	})

	rec.mu.Lock()
	defer rec.mu.Unlock()
	// round 0 + exactly one discussion round (then stable → break) = 4 member spawns.
	if rec.memberStarts != 4 {
		t.Fatalf("member spawns = %d, want 4 (round 0 + one discussion round, early-exit before round 2)", rec.memberStarts)
	}
	if rec.chairStarts != 1 {
		t.Fatalf("chair spawns = %d, want 1", rec.chairStarts)
	}
}

// TestCouncilDiscussionInjectsOtherMembersPositions: the round-1 member prompt
// carries ANOTHER member's round-0 position — verdict + rationale AND, for an edit,
// the actual proposed content — the transcript the engine injects because members
// cannot read each other's candidates via MCP. Without the content a member would
// concur/dissent on an edit it cannot see.
func TestCouncilDiscussionInjectsOtherMembersPositions(t *testing.T) {
	rec := &discRecorder{}
	round0 := CouncilSnapshot{Candidates: []CouncilTurn{
		{AgentIdentity: "member-1", Round: 0, Lens: domain.LensCorrectness, Verdict: domain.VerdictReply, Rationale: "m1-initial-take"},
		{AgentIdentity: "member-2", Round: 0, Lens: domain.LensSkeptic, Verdict: domain.VerdictEdit, Content: "UNIQUE-EDIT-BODY-42", Rationale: "UNIQUE-SKEPTIC-RATIONALE"},
	}}
	state := func(string) (CouncilSnapshot, error) { return round0, nil }
	// rounds=1 ⇒ exactly one discussion round then the loop ends.
	e := New(discConfig(rec, []string{"member-cmd-a", "member-cmd-b"}, "chair-cmd", 1, 0, state))
	defer e.Close()

	e.Fire(webhook.EventCommentCreated, nil)
	waitFor(t, func() bool {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		return rec.chairStarts == 1
	})

	rec.mu.Lock()
	defer rec.mu.Unlock()
	var found bool
	for _, p := range rec.memberPrompts {
		// member-1's round-1 discussion prompt must carry member-2's round-0 position
		// INCLUDING the actual proposed edit content (not just a placeholder).
		if strings.Contains(p, "discussion round 1") &&
			strings.Contains(p, `"member-1"`) &&
			strings.Contains(p, "UNIQUE-SKEPTIC-RATIONALE") &&
			strings.Contains(p, "UNIQUE-EDIT-BODY-42") {
			found = true
		}
	}
	if !found {
		t.Fatalf("round-1 member-1 prompt did not carry member-2's round-0 position + edit content; prompts=%v", rec.memberPrompts)
	}
}

// TestCouncilBudgetCapSkipsRoundsAndLogs: a tiny budget derives a spawn cap that
// permits only round 0, so the discussion rounds are SKIPPED (never a silent
// truncation — the skip is logged) and the chair still runs.
func TestCouncilBudgetCapSkipsRoundsAndLogs(t *testing.T) {
	rec := &discRecorder{}

	var buf bytes.Buffer
	old := log.Writer()
	flags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() { log.SetOutput(old); log.SetFlags(flags) }()

	// budget == approx per-spawn ⇒ cap 1. round 0 already spends 2 (n) spawns, so
	// round 1 (2 + 2 > 1) is skipped for budget even though the state keeps changing.
	e := New(discConfig(rec, []string{"member-cmd-a", "member-cmd-b"}, "chair-cmd", 3, councilApproxTokensPerSpawn, flipState()))
	defer e.Close()

	e.Fire(webhook.EventCommentCreated, nil)
	waitFor(t, func() bool {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		return rec.chairStarts == 1
	})

	rec.mu.Lock()
	ms := rec.memberStarts
	rec.mu.Unlock()
	if ms != 2 {
		t.Fatalf("member spawns = %d, want 2 (budget cap allows round 0 only)", ms)
	}
	if !strings.Contains(buf.String(), "budget cap reached") {
		t.Fatalf("a round skipped for budget must be logged; log was:\n%s", buf.String())
	}
}

// TestCouncilHeartbeatSpansDiscussionRounds: the heartbeat ticker keeps firing for
// the WHOLE multi-round run (round 0 + discussion rounds + chair), so a long run
// never lets the claimed comment go stale mid-run.
func TestCouncilHeartbeatSpansDiscussionRounds(t *testing.T) {
	rec := &discRecorder{}
	rec.block = 30 * time.Millisecond // each of the 3 member waves takes ~30ms ⇒ ~90ms run
	var hb int32
	cfg := discConfig(rec, []string{"member-cmd-a", "member-cmd-b"}, "chair-cmd", 2, 0, flipState())
	cfg.HeartbeatInterval = 15 * time.Millisecond
	cfg.Heartbeat = func(string, string) error { atomic.AddInt32(&hb, 1); return nil }
	e := New(cfg)
	defer e.Close()

	e.Fire(webhook.EventCommentCreated, nil)
	waitFor(t, func() bool {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		return rec.chairStarts == 1
	})
	// Immediate beat + ticks across the ~90ms three-wave run.
	if got := atomic.LoadInt32(&hb); got < 3 {
		t.Fatalf("heartbeat fired %d times across the multi-round run, want >= 3", got)
	}
	// The ticker must stop when the run ends (no leak): the count freezes.
	time.Sleep(10 * time.Millisecond)
	frozen := atomic.LoadInt32(&hb)
	time.Sleep(80 * time.Millisecond)
	if got := atomic.LoadInt32(&hb); got != frozen {
		t.Fatalf("heartbeat kept firing after the run (%d → %d): ticker did not stop", frozen, got)
	}
}

// TestCouncilNoCouncilStateSkipsDiscussion: with the other council seams wired but
// CouncilState nil, the council runs exactly as before — round-0 blind reviews then
// the chair, NO discussion rounds — so wiring this slice is opt-in and safe.
func TestCouncilNoCouncilStateSkipsDiscussion(t *testing.T) {
	rec := &discRecorder{}
	cfg := discConfig(rec, []string{"member-cmd-a", "member-cmd-b"}, "chair-cmd", 3, 0, flipState())
	cfg.CouncilState = nil // discussion disabled
	e := New(cfg)
	defer e.Close()

	e.Fire(webhook.EventCommentCreated, nil)
	waitFor(t, func() bool {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		return rec.chairStarts == 1
	})
	time.Sleep(40 * time.Millisecond)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.memberStarts != 2 || rec.chairStarts != 1 {
		t.Fatalf("no CouncilState: memberStarts=%d chairStarts=%d, want 2/1 (round 0 + chair, no discussion)", rec.memberStarts, rec.chairStarts)
	}
}
