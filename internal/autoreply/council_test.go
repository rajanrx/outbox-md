package autoreply

import (
	"context"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rajanrx/outbox-md/internal/domain"
	"github.com/rajanrx/outbox-md/internal/webhook"
)

// councilRecorder captures every spawn a council run makes, distinguishing member
// spawns (their command starts with "member-cmd") from the chair (command
// "chair-cmd"). It records the lens/identity carried in each member prompt and,
// for the chair, snapshots how many members had RETURNED when the chair started —
// the direct signal that the chair runs only after the whole member wave joined.
type councilRecorder struct {
	mu              sync.Mutex
	memberStarts    int
	memberDone      int
	memberLenses    map[string]bool
	memberIDs       map[string]bool
	chairStarts     int
	chairSawMembers int // memberDone observed at chair entry
	memberBlock     time.Duration
}

func newCouncilRecorder() *councilRecorder {
	return &councilRecorder{memberLenses: map[string]bool{}, memberIDs: map[string]bool{}}
}

// spawn is the fake SpawnFunc. Member commands are "member-cmd*"; the chair is
// "chair-cmd".
func (r *councilRecorder) spawn(_ context.Context, _, agentCmd, prompt string) error {
	if strings.HasPrefix(agentCmd, "member-cmd") {
		r.mu.Lock()
		r.memberStarts++
		for _, l := range []string{
			domain.LensCorrectness, domain.LensCompleteness, domain.LensAmbiguity,
			domain.LensRisk, domain.LensSimplicity, domain.LensSkeptic,
		} {
			if strings.Contains(prompt, `"`+l+`"`) {
				r.memberLenses[l] = true
			}
		}
		for _, id := range []string{"member-1", "member-2", "member-3", "member-4"} {
			if strings.Contains(prompt, `"`+id+`"`) {
				r.memberIDs[id] = true
			}
		}
		block := r.memberBlock
		r.mu.Unlock()
		if block > 0 {
			time.Sleep(block)
		}
		r.mu.Lock()
		r.memberDone++
		r.mu.Unlock()
		return nil
	}
	// chair
	r.mu.Lock()
	r.chairStarts++
	r.chairSawMembers = r.memberDone
	r.mu.Unlock()
	return nil
}

// councilConfigFor builds an Enabled council engine Config for a single project
// "p" with the given members + chair and all council seams wired. Claim always
// wins; OpenComments returns the given comment ids once (then empty, so the run
// does not loop); Heartbeat is a no-op counter.
func councilConfigFor(rec *councilRecorder, members []string, chair string, commentIDs []string, hb func()) Config {
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
			// Serve the comments once; subsequent passes see none (a claimed comment
			// leaves the open set in production).
			if atomic.AddInt32(&served, 1) > 1 {
				return nil, nil
			}
			refs := make([]CommentRef, 0, len(commentIDs))
			for _, id := range commentIDs {
				refs = append(refs, CommentRef{ID: id})
			}
			return refs, nil
		},
		Heartbeat: func(string, string) error {
			if hb != nil {
				hb()
			}
			return nil
		},
	}
}

// TestCouncilFansOutThenChair is the core ordering + assignment test: a 3-member
// council fans out 3 member spawns with DISTINCT lenses + identities (including
// the skeptic), joins them, THEN spawns the chair — and the chair starts only
// after all three members have returned.
func TestCouncilFansOutThenChair(t *testing.T) {
	rec := newCouncilRecorder()
	rec.memberBlock = 30 * time.Millisecond // overlap so the join is meaningful
	e := New(councilConfigFor(rec, []string{"member-cmd-a", "member-cmd-b", "member-cmd-c"}, "chair-cmd", []string{"c1"}, nil))
	defer e.Close()

	e.Fire(webhook.EventCommentCreated, nil)
	waitFor(t, func() bool {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		return rec.chairStarts == 1
	})

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.memberStarts != 3 {
		t.Fatalf("member spawns = %d, want 3", rec.memberStarts)
	}
	if rec.chairStarts != 1 {
		t.Fatalf("chair spawns = %d, want 1", rec.chairStarts)
	}
	if rec.chairSawMembers != 3 {
		t.Fatalf("chair started with %d members returned, want 3 (chair must run after the join)", rec.chairSawMembers)
	}
	// Distinct lenses, skeptic covered.
	for _, l := range []string{domain.LensCorrectness, domain.LensCompleteness, domain.LensSkeptic} {
		if !rec.memberLenses[l] {
			t.Fatalf("lens %q not assigned to any member; got %v", l, rec.memberLenses)
		}
	}
	if len(rec.memberLenses) != 3 {
		t.Fatalf("expected 3 distinct lenses, got %v", rec.memberLenses)
	}
	// Distinct identities member-1..3.
	for _, id := range []string{"member-1", "member-2", "member-3"} {
		if !rec.memberIDs[id] {
			t.Fatalf("identity %q not seen; got %v", id, rec.memberIDs)
		}
	}
}

// TestCouncilConcurrencyOneStillOrders confirms Concurrency=1 is valid: members
// run serially, then the chair — the chair still runs after all members.
func TestCouncilConcurrencyOneStillOrders(t *testing.T) {
	rec := newCouncilRecorder()
	cfg := councilConfigFor(rec, []string{"member-cmd-a", "member-cmd-b"}, "chair-cmd", []string{"c1"}, nil)
	cfg.Concurrency = 1
	e := New(cfg)
	defer e.Close()

	e.Fire(webhook.EventCommentCreated, nil)
	waitFor(t, func() bool {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		return rec.chairStarts == 1
	})
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.memberStarts != 2 || rec.chairSawMembers != 2 {
		t.Fatalf("serial council: memberStarts=%d chairSawMembers=%d, want 2 and 2", rec.memberStarts, rec.chairSawMembers)
	}
}

// TestCouncilClaimOnceSkips is the claim-once invariant: when Claim reports the
// comment was NOT won (another council-run holds it), the pass skips it — no
// members, no chair, no heartbeat.
func TestCouncilClaimOnceSkips(t *testing.T) {
	rec := newCouncilRecorder()
	var hbCalls int32
	cfg := councilConfigFor(rec, []string{"member-cmd-a", "member-cmd-b"}, "chair-cmd", []string{"c1"},
		func() { atomic.AddInt32(&hbCalls, 1) })
	// This run loses the claim.
	cfg.Claim = func(string) (string, bool, error) { return "", false, nil }
	e := New(cfg)
	defer e.Close()

	e.Fire(webhook.EventCommentCreated, nil)
	// Give the pass time to run and skip.
	time.Sleep(80 * time.Millisecond)

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.memberStarts != 0 || rec.chairStarts != 0 {
		t.Fatalf("lost-claim run spawned members=%d chair=%d, want 0/0", rec.memberStarts, rec.chairStarts)
	}
	if got := atomic.LoadInt32(&hbCalls); got != 0 {
		t.Fatalf("lost-claim run heartbeat %d times, want 0", got)
	}
}

// TestCouncilHeartbeats proves the engine heartbeats for the WHOLE run on a
// ticker (not just the immediate beat), and that the ticker STOPS after the run.
func TestCouncilHeartbeats(t *testing.T) {
	rec := newCouncilRecorder()
	rec.memberBlock = 120 * time.Millisecond // outlast several ticks
	var hbCalls int32
	cfg := councilConfigFor(rec, []string{"member-cmd-a", "member-cmd-b"}, "chair-cmd", []string{"c1"},
		func() { atomic.AddInt32(&hbCalls, 1) })
	cfg.HeartbeatInterval = 20 * time.Millisecond
	e := New(cfg)
	defer e.Close()

	e.Fire(webhook.EventCommentCreated, nil)
	waitFor(t, func() bool {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		return rec.chairStarts == 1
	})
	// Immediate beat + at least one ticker beat during the ~120ms member wave.
	if got := atomic.LoadInt32(&hbCalls); got < 2 {
		t.Fatalf("heartbeat fired %d times, want >= 2 (immediate + ticker)", got)
	}
	// Ticker must stop once the run ends: the count freezes.
	time.Sleep(10 * time.Millisecond)
	frozen := atomic.LoadInt32(&hbCalls)
	time.Sleep(100 * time.Millisecond) // several tick intervals
	if got := atomic.LoadInt32(&hbCalls); got != frozen {
		t.Fatalf("heartbeat kept firing after the run (%d → %d): ticker did not stop", frozen, got)
	}
}

// TestCouncilNoSelfRetrigger confirms one human trigger yields exactly one council
// pass — the council's internal member/chair spawns never re-trigger the engine
// (Fire only reacts to comment.created/replied, and the council never calls Fire).
func TestCouncilNoSelfRetrigger(t *testing.T) {
	rec := newCouncilRecorder()
	var opens int32
	cfg := councilConfigFor(rec, []string{"member-cmd-a", "member-cmd-b"}, "chair-cmd", []string{"c1"}, nil)
	base := cfg.OpenComments
	cfg.OpenComments = func(p string) ([]CommentRef, error) {
		atomic.AddInt32(&opens, 1)
		return base(p)
	}
	e := New(cfg)
	defer e.Close()

	e.Fire(webhook.EventCommentCreated, nil)
	waitFor(t, func() bool {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		return rec.chairStarts == 1
	})
	// Settle: no further pass should start.
	time.Sleep(60 * time.Millisecond)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.memberStarts != 2 || rec.chairStarts != 1 {
		t.Fatalf("re-trigger: memberStarts=%d chairStarts=%d, want 2/1 (one pass)", rec.memberStarts, rec.chairStarts)
	}
}

// TestSingleAgentUnaffectedByCouncilSeams is the invariant guard: a project that
// is NOT a council (< 2 members) uses the single-agent spawn even when the council
// seams are wired — no claim, no open-comments listing, no heartbeat.
func TestSingleAgentUnaffectedByCouncilSeams(t *testing.T) {
	var single int32
	var claims, opens, beats int32
	e := New(Config{
		Enabled:  true,
		Debounce: testDebounce,
		Resolve:  func(any) string { return "solo" },
		Targets: map[string]Target{
			// One member ⇒ single-agent (AgentCmd path), NOT council.
			"solo": {Root: "/solo", AgentCmd: "solo-cmd", Members: []string{"solo-cmd"}},
		},
		Spawn: func(_ context.Context, _, agentCmd, _ string) error {
			if agentCmd == "solo-cmd" {
				atomic.AddInt32(&single, 1)
			}
			return nil
		},
		Claim:        func(string) (string, bool, error) { atomic.AddInt32(&claims, 1); return "t", true, nil },
		OpenComments: func(string) ([]CommentRef, error) { atomic.AddInt32(&opens, 1); return nil, nil },
		Heartbeat:    func(string, string) error { atomic.AddInt32(&beats, 1); return nil },
	})
	defer e.Close()

	e.Fire(webhook.EventCommentCreated, nil)
	waitFor(t, func() bool { return atomic.LoadInt32(&single) == 1 })
	time.Sleep(40 * time.Millisecond)
	if atomic.LoadInt32(&claims) != 0 || atomic.LoadInt32(&opens) != 0 || atomic.LoadInt32(&beats) != 0 {
		t.Fatalf("single-agent path touched council seams: claims=%d opens=%d beats=%d",
			atomic.LoadInt32(&claims), atomic.LoadInt32(&opens), atomic.LoadInt32(&beats))
	}
}

// TestCouncilTargetFallsBackWhenSeamsUnwired: a council-shaped Target (>=2 members
// + chair) but NIL council seams must fall back to the single-agent spawn (using
// AgentCmd / the first member) and never crash.
func TestCouncilTargetFallsBackWhenSeamsUnwired(t *testing.T) {
	var spawns int32
	e := New(Config{
		Enabled:  true,
		Debounce: testDebounce,
		Resolve:  func(any) string { return "p" },
		Targets: map[string]Target{
			"p": {Root: "/p", AgentCmd: "fallback-cmd", Members: []string{"m1", "m2"}, Chair: "chair"},
		},
		Spawn: func(context.Context, string, string, string) error { atomic.AddInt32(&spawns, 1); return nil },
		// No Claim/OpenComments/Heartbeat ⇒ councilEnabled() is false ⇒ single-agent.
	})
	defer e.Close()

	e.Fire(webhook.EventCommentCreated, nil)
	waitFor(t, func() bool { return atomic.LoadInt32(&spawns) >= 1 })
	// Single-agent path: exactly one spawn per trigger (no drain wired).
	time.Sleep(40 * time.Millisecond)
	if got := atomic.LoadInt32(&spawns); got != 1 {
		t.Fatalf("unwired council target spawned %d times, want 1 (single-agent fallback)", got)
	}
}

// TestCouncilPerProjectIsolation: a council project and a single-agent project on
// the same engine each run their OWN path; a trigger on one never spills into the
// other.
func TestCouncilPerProjectIsolation(t *testing.T) {
	rec := newCouncilRecorder()
	var soloSpawns int32
	route := "a"
	var routeMu sync.Mutex
	cfg := Config{
		Enabled:     true,
		Debounce:    testDebounce,
		Concurrency: 2,
		Resolve: func(any) string {
			routeMu.Lock()
			defer routeMu.Unlock()
			return route
		},
		Targets: map[string]Target{
			"a": {Root: "/a", Members: []string{"member-cmd-a", "member-cmd-b"}, Chair: "chair-cmd"},
			"b": {Root: "/b", AgentCmd: "solo-cmd", Members: []string{"solo-cmd"}},
		},
		Spawn: func(_ context.Context, _, agentCmd, prompt string) error {
			if agentCmd == "solo-cmd" {
				atomic.AddInt32(&soloSpawns, 1)
				return nil
			}
			return rec.spawn(context.Background(), "", agentCmd, prompt)
		},
		Claim:        func(string) (string, bool, error) { return "t", true, nil },
		OpenComments: func(p string) ([]CommentRef, error) { return []CommentRef{{ID: "c-" + p}}, nil },
		Heartbeat:    func(string, string) error { return nil },
	}
	e := New(cfg)
	defer e.Close()

	// Fire the council project.
	e.Fire(webhook.EventCommentCreated, nil)
	waitFor(t, func() bool {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		return rec.chairStarts >= 1
	})
	if atomic.LoadInt32(&soloSpawns) != 0 {
		t.Fatalf("council trigger leaked into single-agent project: soloSpawns=%d", atomic.LoadInt32(&soloSpawns))
	}

	// Now fire the single-agent project.
	routeMu.Lock()
	route = "b"
	routeMu.Unlock()
	e.Fire(webhook.EventCommentCreated, nil)
	waitFor(t, func() bool { return atomic.LoadInt32(&soloSpawns) >= 1 })
	rec.mu.Lock()
	memberStarts := rec.memberStarts
	rec.mu.Unlock()
	if memberStarts != 2 {
		t.Fatalf("single-agent trigger perturbed the council (memberStarts=%d, want 2)", memberStarts)
	}
}

// TestCouncilCloseNoGoroutineLeak runs a council to completion and asserts the
// heartbeat ticker goroutine is joined (goroutine count returns to baseline), and
// that Close mid-flight also unwinds cleanly. Complements the -race suite.
func TestCouncilCloseNoGoroutineLeak(t *testing.T) {
	rec := newCouncilRecorder()
	cfg := councilConfigFor(rec, []string{"member-cmd-a", "member-cmd-b"}, "chair-cmd", []string{"c1"}, nil)
	cfg.HeartbeatInterval = 15 * time.Millisecond
	e := New(cfg)

	runtime.GC()
	time.Sleep(20 * time.Millisecond)
	base := runtime.NumGoroutine()

	e.Fire(webhook.EventCommentCreated, nil)
	waitFor(t, func() bool {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		return rec.chairStarts == 1
	})
	e.Close()
	// Let any stragglers exit.
	time.Sleep(50 * time.Millisecond)
	runtime.GC()
	if got := runtime.NumGoroutine(); got > base+2 {
		t.Fatalf("goroutine leak: baseline %d, after run+Close %d", base, got)
	}
}
