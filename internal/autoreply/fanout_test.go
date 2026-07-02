package autoreply

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rajanrx/outbox-md/internal/webhook"
)

// decTo0 decrements a counter toward a floor of zero (mirrors the drain tests).
func decTo0(p *int64) {
	for {
		n := atomic.LoadInt64(p)
		if n == 0 {
			return
		}
		if atomic.CompareAndSwapInt64(p, n, n-1) {
			return
		}
	}
}

// TestConcurrencyRunsInParallel: with concurrency=4 and >=4 pending, one wave
// launches 4 agent runs AT ONCE. A barrier proves all four are genuinely in
// flight simultaneously (maxSeen == 4), which single-flight could never reach.
func TestConcurrencyRunsInParallel(t *testing.T) {
	var remaining int64 = 4
	var mu sync.Mutex
	var concurrent, maxSeen, total int
	started := make(chan struct{}, 8)
	release := make(chan struct{})

	e := New(Config{
		Enabled:     true,
		Debounce:    testDebounce,
		DrainDelay:  time.Millisecond,
		Concurrency: 4,
		Spawn: func(context.Context, string, string, string) error {
			mu.Lock()
			concurrent++
			total++
			if concurrent > maxSeen {
				maxSeen = concurrent
			}
			mu.Unlock()
			started <- struct{}{}
			<-release // hold so the whole wave is in flight together
			mu.Lock()
			concurrent--
			mu.Unlock()
			decTo0(&remaining)
			return nil
		},
		PendingCount: func(string) (int, error) { return int(atomic.LoadInt64(&remaining)), nil },
	})

	e.Fire(webhook.EventCommentCreated, webhook.Event{})
	// Wait until all four runs are simultaneously blocked on release.
	for i := 0; i < 4; i++ {
		<-started
	}
	close(release)

	waitFor(t, func() bool { return atomic.LoadInt64(&remaining) == 0 })
	time.Sleep(40 * time.Millisecond) // let any stray wave appear

	mu.Lock()
	defer mu.Unlock()
	if maxSeen != 4 {
		t.Fatalf("max concurrent spawns = %d, want 4 (fan-out did not run in parallel)", maxSeen)
	}
	if total != 4 {
		t.Fatalf("total spawns = %d, want 4 (one wave clears the 4-comment backlog)", total)
	}
}

// TestConcurrencyOneIsSingleFlight: concurrency=1 must be bit-for-bit today's
// behaviour — never more than one run at a time, and the drain still clears the
// backlog one comment per sequential run. This is the no-regression guard.
func TestConcurrencyOneIsSingleFlight(t *testing.T) {
	var remaining int64 = 4
	var mu sync.Mutex
	var concurrent, maxSeen int
	var total int64

	e := New(Config{
		Enabled:     true,
		Debounce:    testDebounce,
		DrainDelay:  time.Millisecond,
		Concurrency: 1,
		Spawn: func(context.Context, string, string, string) error {
			mu.Lock()
			concurrent++
			if concurrent > maxSeen {
				maxSeen = concurrent
			}
			mu.Unlock()
			atomic.AddInt64(&total, 1)
			time.Sleep(2 * time.Millisecond) // widen the window a real overlap would show in
			mu.Lock()
			concurrent--
			mu.Unlock()
			decTo0(&remaining)
			return nil
		},
		PendingCount: func(string) (int, error) { return int(atomic.LoadInt64(&remaining)), nil },
	})

	e.Fire(webhook.EventCommentCreated, webhook.Event{})
	waitFor(t, func() bool { return atomic.LoadInt64(&remaining) == 0 })
	time.Sleep(30 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if maxSeen != 1 {
		t.Fatalf("max concurrent spawns = %d, want 1 (concurrency=1 must stay single-flight)", maxSeen)
	}
	if got := atomic.LoadInt64(&total); got != 4 {
		t.Fatalf("total spawns = %d, want 4 (one sequential run per backlog comment)", got)
	}
}

// TestDrainProgressAcrossConcurrentWave: with concurrency=2 and a 4-comment
// backlog, progress is accounted PER WAVE (before vs after the whole wave), not
// per run — so two waves of two runs clear it, and the drain stops at zero.
func TestDrainProgressAcrossConcurrentWave(t *testing.T) {
	var remaining int64 = 4
	var spawns int64

	e := New(Config{
		Enabled:     true,
		Debounce:    testDebounce,
		DrainDelay:  time.Millisecond,
		Concurrency: 2,
		Spawn: func(context.Context, string, string, string) error {
			atomic.AddInt64(&spawns, 1)
			decTo0(&remaining) // each run finishes exactly one comment
			return nil
		},
		PendingCount: func(string) (int, error) { return int(atomic.LoadInt64(&remaining)), nil },
	})

	e.Fire(webhook.EventCommentCreated, webhook.Event{})
	waitFor(t, func() bool { return atomic.LoadInt64(&remaining) == 0 })
	time.Sleep(40 * time.Millisecond)
	if got := atomic.LoadInt64(&spawns); got != 4 {
		t.Fatalf("spawns = %d, want 4 (2 waves x 2 runs drain the 4-comment backlog)", got)
	}
}

// TestConcurrencyNoSelfRetrigger: fan-out does not weaken the no-self-retrigger
// invariant — the agent's own writes must still never trigger a run, even with a
// pool of 4.
func TestConcurrencyNoSelfRetrigger(t *testing.T) {
	var spawns int32
	e := New(Config{
		Enabled:     true,
		Debounce:    testDebounce,
		Concurrency: 4,
		Spawn: func(context.Context, string, string, string) error {
			atomic.AddInt32(&spawns, 1)
			return nil
		},
		PendingCount: func(string) (int, error) { return 4, nil },
	})
	for _, event := range []string{
		webhook.EventCommentUpdated,
		webhook.EventSuggestionProposed,
		webhook.EventCommentProcessing,
		webhook.EventCommentResolved,
		webhook.EventDocumentApprove,
	} {
		e.Fire(event, webhook.Event{})
	}
	time.Sleep(60 * time.Millisecond)
	if got := atomic.LoadInt32(&spawns); got != 0 {
		t.Fatalf("agent events spawned %d runs under fan-out, want 0 (self-retrigger!)", got)
	}
}

// TestConcurrencyPerProjectIsolation: a burst on project A never spawns work for
// project B — each project has its own pool and drain.
func TestConcurrencyPerProjectIsolation(t *testing.T) {
	var aSpawns, bSpawns int64
	var aRemaining int64 = 4
	e := New(Config{
		Enabled:     true,
		Debounce:    testDebounce,
		DrainDelay:  time.Millisecond,
		Concurrency: 4,
		Targets: map[string]Target{
			"a": {Root: "/proj/a"},
			"b": {Root: "/proj/b"},
		},
		Spawn: func(_ context.Context, dir, _, _ string) error {
			if dir == "/proj/a" {
				atomic.AddInt64(&aSpawns, 1)
				decTo0(&aRemaining)
			} else if dir == "/proj/b" {
				atomic.AddInt64(&bSpawns, 1)
			}
			return nil
		},
		PendingCount: func(project string) (int, error) {
			if project == "a" {
				return int(atomic.LoadInt64(&aRemaining)), nil
			}
			return 0, nil
		},
	})

	e.Fire(webhook.EventCommentCreated, webhook.Event{Project: "a"})
	waitFor(t, func() bool { return atomic.LoadInt64(&aSpawns) >= 1 })
	time.Sleep(40 * time.Millisecond)
	if got := atomic.LoadInt64(&bSpawns); got != 0 {
		t.Fatalf("project B spawned %d runs from a project-A burst, want 0 (isolation broken)", got)
	}
	if got := atomic.LoadInt64(&aSpawns); got == 0 {
		t.Fatal("project A never spawned, want >=1")
	}
}
