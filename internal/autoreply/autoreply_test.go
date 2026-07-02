package autoreply

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rajanrx/outbox-md/internal/webhook"
)

// testDebounce is short so tests never wait a real 1.5s.
const testDebounce = 10 * time.Millisecond

// waitFor polls cond until true or the deadline, so tests don't race on the
// background goroutine that runs the spawn.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not met before deadline")
}

func TestEnabledReflectsConfig(t *testing.T) {
	if New(Config{Enabled: false}).Enabled() {
		t.Fatal("Enabled() should be false when off")
	}
	if !New(Config{Enabled: true}).Enabled() {
		t.Fatal("Enabled() should be true when on")
	}
}

func TestDisabledEngineNeverSpawns(t *testing.T) {
	var calls int32
	e := New(Config{
		Enabled:  false,
		Debounce: testDebounce,
		Spawn: func(context.Context, string, string, string) error {
			atomic.AddInt32(&calls, 1)
			return nil
		},
	})
	e.Fire(webhook.EventCommentCreated, nil)
	time.Sleep(50 * time.Millisecond)
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Fatalf("disabled engine spawned %d times, want 0", got)
	}
}

func TestHumanEventsTrigger(t *testing.T) {
	for _, event := range []string{webhook.EventCommentCreated, webhook.EventCommentReplied} {
		var calls int32
		e := New(Config{
			Enabled:  true,
			Debounce: testDebounce,
			Spawn: func(context.Context, string, string, string) error {
				atomic.AddInt32(&calls, 1)
				return nil
			},
		})
		e.Fire(event, nil)
		waitFor(t, func() bool { return atomic.LoadInt32(&calls) == 1 })
	}
}

// TestAgentEventsDoNotTrigger is the no-self-retrigger invariant: the agent's
// own writes must never trigger a run, or the agent loops forever.
func TestAgentEventsDoNotTrigger(t *testing.T) {
	for _, event := range []string{
		webhook.EventCommentUpdated,
		webhook.EventSuggestionProposed,
		webhook.EventCommentProcessing,
		webhook.EventCommentResolved,
		webhook.EventDocumentApprove,
	} {
		var calls int32
		e := New(Config{
			Enabled:  true,
			Debounce: testDebounce,
			Spawn: func(context.Context, string, string, string) error {
				atomic.AddInt32(&calls, 1)
				return nil
			},
		})
		e.Fire(event, nil)
		time.Sleep(50 * time.Millisecond)
		if got := atomic.LoadInt32(&calls); got != 0 {
			t.Fatalf("event %q spawned %d times, want 0 (self-retrigger!)", event, got)
		}
	}
}

// TestDebounceCoalescesBurst: a burst of triggers within the window collapses
// into exactly one run.
func TestDebounceCoalescesBurst(t *testing.T) {
	var calls int32
	e := New(Config{
		Enabled:  true,
		Debounce: 40 * time.Millisecond,
		Spawn: func(context.Context, string, string, string) error {
			atomic.AddInt32(&calls, 1)
			return nil
		},
	})
	for i := 0; i < 5; i++ {
		e.Fire(webhook.EventCommentCreated, nil)
		time.Sleep(5 * time.Millisecond)
	}
	waitFor(t, func() bool { return atomic.LoadInt32(&calls) == 1 })
	// Give any erroneously-scheduled extra runs time to fire, then assert one.
	time.Sleep(80 * time.Millisecond)
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("burst coalesced to %d runs, want 1", got)
	}
}

// TestSingleFlightSerializes: a trigger arriving while a run is in flight yields
// exactly one follow-up run — never two concurrent processes.
func TestSingleFlightSerializes(t *testing.T) {
	var (
		mu         sync.Mutex
		concurrent int
		maxSeen    int
		total      int
	)
	release := make(chan struct{})
	started := make(chan struct{}, 8)
	e := New(Config{
		Enabled:  true,
		Debounce: testDebounce,
		Spawn: func(context.Context, string, string, string) error {
			mu.Lock()
			concurrent++
			total++
			if concurrent > maxSeen {
				maxSeen = concurrent
			}
			mu.Unlock()
			started <- struct{}{}
			<-release // block so the run is genuinely in flight
			mu.Lock()
			concurrent--
			mu.Unlock()
			return nil
		},
	})

	// First trigger → run starts and blocks on release.
	e.Fire(webhook.EventCommentCreated, nil)
	<-started
	// Fire several more while the first run is blocked: they must collapse into
	// exactly ONE follow-up run, not start concurrent processes.
	for i := 0; i < 4; i++ {
		e.Fire(webhook.EventCommentReplied, nil)
	}
	time.Sleep(50 * time.Millisecond) // let debounce fire while run #1 is in flight

	close(release) // unblock: run #1 finishes, the pending follow-up runs once
	<-started      // follow-up run starts

	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return total == 2 && concurrent == 0
	})
	mu.Lock()
	defer mu.Unlock()
	if maxSeen != 1 {
		t.Fatalf("max concurrent spawns = %d, want 1 (single-flight violated)", maxSeen)
	}
	if total != 2 {
		t.Fatalf("total runs = %d, want 2 (one initial + one coalesced follow-up)", total)
	}
}

// TestSpawnReceivesConfig confirms the injected spawn is handed the served dir,
// command template, and prompt.
func TestSpawnReceivesConfig(t *testing.T) {
	got := make(chan [3]string, 1)
	e := New(Config{
		Enabled:  true,
		Dir:      "/served/dir",
		AgentCmd: "fake -p {prompt}",
		Prompt:   "do the thing",
		Debounce: testDebounce,
		Spawn: func(_ context.Context, dir, cmd, prompt string) error {
			got <- [3]string{dir, cmd, prompt}
			return nil
		},
	})
	e.Fire(webhook.EventCommentCreated, nil)
	select {
	case v := <-got:
		if v[0] != "/served/dir" || v[1] != "fake -p {prompt}" || v[2] != "do the thing" {
			t.Fatalf("spawn got %v, want served dir/cmd/prompt", v)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("spawn was not called")
	}
}

// TestSpawnPanicDoesNotCrash: a panicking spawn is recovered, not fatal, and a
// subsequent trigger still runs.
func TestSpawnPanicDoesNotCrash(t *testing.T) {
	var calls int32
	e := New(Config{
		Enabled:  true,
		Debounce: testDebounce,
		Spawn: func(context.Context, string, string, string) error {
			if atomic.AddInt32(&calls, 1) == 1 {
				panic("boom")
			}
			return nil
		},
	})
	e.Fire(webhook.EventCommentCreated, nil)
	waitFor(t, func() bool { return atomic.LoadInt32(&calls) >= 1 })
	// A later trigger still works — the engine survived the panic.
	e.Fire(webhook.EventCommentCreated, nil)
	waitFor(t, func() bool { return atomic.LoadInt32(&calls) == 2 })
}

func TestBuildArgsSubstitutesPromptAsSingleArg(t *testing.T) {
	args := buildArgs("claude -p {prompt} --allowedTools mcp__outbox-md__*", "multi word prompt")
	want := []string{"claude", "-p", "multi word prompt", "--allowedTools", "mcp__outbox-md__*"}
	if len(args) != len(want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("args[%d] = %q, want %q", i, args[i], want[i])
		}
	}
}

func TestDefaultsApplied(t *testing.T) {
	e := New(Config{Enabled: true})
	if e.prompt != DefaultPrompt {
		t.Error("empty prompt should default to DefaultPrompt")
	}
	if e.debounce != DefaultDebounce {
		t.Error("zero debounce should default to DefaultDebounce")
	}
	if e.timeout != DefaultTimeout {
		t.Error("zero timeout should default to DefaultTimeout")
	}
	if e.spawn == nil {
		t.Error("nil spawn should default to SpawnCLI")
	}
}

// TestPerProjectResolvesRootAndAgent is the key multi-project invariant: a
// triggering comment on project A spawns the agent with cwd == A's root and A's
// agent command; a comment on project B spawns with B's root + agent. The two
// runners are independent.
func TestPerProjectResolvesRootAndAgent(t *testing.T) {
	got := make(chan [2]string, 4)
	e := New(Config{
		Enabled:  true,
		Dir:      "/default/root",
		AgentCmd: "default -p {prompt}",
		Debounce: testDebounce,
		Targets: map[string]Target{
			"alpha": {Root: "/repos/alpha", AgentCmd: "claude -p {prompt}"},
			"beta":  {Root: "/repos/beta", AgentCmd: "codex exec {prompt}"},
		},
		Spawn: func(_ context.Context, dir, cmd, _ string) error {
			got <- [2]string{dir, cmd}
			return nil
		},
	})

	e.Fire(webhook.EventCommentCreated, webhook.Event{Project: "alpha"})
	if v := <-got; v[0] != "/repos/alpha" || v[1] != "claude -p {prompt}" {
		t.Fatalf("alpha spawn = %v, want /repos/alpha + claude", v)
	}
	e.Fire(webhook.EventCommentReplied, webhook.Event{Project: "beta"})
	if v := <-got; v[0] != "/repos/beta" || v[1] != "codex exec {prompt}" {
		t.Fatalf("beta spawn = %v, want /repos/beta + codex", v)
	}
}

// TestFallbackToDefaults verifies the fallback chain: an unknown project uses
// the engine's default Dir/AgentCmd, and a Target with an empty AgentCmd uses
// the default command while keeping its own root.
func TestFallbackToDefaults(t *testing.T) {
	got := make(chan [2]string, 4)
	e := New(Config{
		Enabled:  true,
		Dir:      "/default/root",
		AgentCmd: "default -p {prompt}",
		Debounce: testDebounce,
		Targets: map[string]Target{
			// Root set, agent empty → inherit the default command.
			"gamma": {Root: "/repos/gamma"},
		},
		Spawn: func(_ context.Context, dir, cmd, _ string) error {
			got <- [2]string{dir, cmd}
			return nil
		},
	})

	// Unknown project → default root + default command.
	e.Fire(webhook.EventCommentCreated, webhook.Event{Project: "unknown"})
	if v := <-got; v[0] != "/default/root" || v[1] != "default -p {prompt}" {
		t.Fatalf("unknown project spawn = %v, want defaults", v)
	}
	// Known project with empty agent → its root + default command.
	e.Fire(webhook.EventCommentCreated, webhook.Event{Project: "gamma"})
	if v := <-got; v[0] != "/repos/gamma" || v[1] != "default -p {prompt}" {
		t.Fatalf("gamma spawn = %v, want /repos/gamma + default cmd", v)
	}
}

// TestSingleFolderEmptyProject verifies single-folder mode (project "" with no
// Targets) spawns in the default dir — bit-for-bit the old single-cwd behaviour.
func TestSingleFolderEmptyProject(t *testing.T) {
	got := make(chan string, 1)
	e := New(Config{
		Enabled:  true,
		Dir:      "/served/dir",
		AgentCmd: "claude -p {prompt}",
		Debounce: testDebounce,
		Spawn: func(_ context.Context, dir, _, _ string) error {
			got <- dir
			return nil
		},
	})
	e.Fire(webhook.EventCommentCreated, webhook.Event{Project: ""})
	if dir := <-got; dir != "/served/dir" {
		t.Fatalf("single-folder spawn dir = %q, want /served/dir", dir)
	}
}

// TestPerProjectNoSelfRetrigger confirms the no-self-retrigger invariant still
// holds with a project-carrying payload: an agent-action event never spawns.
func TestPerProjectNoSelfRetrigger(t *testing.T) {
	var calls int32
	e := New(Config{
		Enabled:  true,
		Debounce: testDebounce,
		Targets:  map[string]Target{"alpha": {Root: "/repos/alpha", AgentCmd: "x {prompt}"}},
		Spawn: func(context.Context, string, string, string) error {
			atomic.AddInt32(&calls, 1)
			return nil
		},
	})
	e.Fire(webhook.EventSuggestionProposed, webhook.Event{Project: "alpha"})
	e.Fire(webhook.EventCommentUpdated, webhook.Event{Project: "alpha"})
	time.Sleep(50 * time.Millisecond)
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Fatalf("agent-action events spawned %d times, want 0", got)
	}
}

// TestDrainClearsPartialBurst: one human trigger, an agent that clears only ONE
// comment per run, and a PendingCount that reflects the shrinking backlog. The
// engine must drain the queue — running again while each run makes progress —
// until nothing remains, without any further human events.
func TestDrainClearsPartialBurst(t *testing.T) {
	var remaining int64 = 3
	var spawns int64
	e := New(Config{
		Enabled:    true,
		Debounce:   testDebounce,
		DrainDelay: time.Millisecond,
		Spawn: func(context.Context, string, string, string) error {
			atomic.AddInt64(&spawns, 1)
			// Simulate an agent that finishes exactly one comment this run.
			for {
				n := atomic.LoadInt64(&remaining)
				if n == 0 {
					return nil
				}
				if atomic.CompareAndSwapInt64(&remaining, n, n-1) {
					return nil
				}
			}
		},
		PendingCount: func(string) (int, error) { return int(atomic.LoadInt64(&remaining)), nil },
	})
	e.Fire(webhook.EventCommentCreated, webhook.Event{})
	waitFor(t, func() bool { return atomic.LoadInt64(&remaining) == 0 })
	// Exactly one run per comment: the drain kept going while progress was made.
	if got := atomic.LoadInt64(&spawns); got != 3 {
		t.Fatalf("spawns = %d, want 3 (one drain run per backlog comment)", got)
	}
	// Give any erroneous extra drain run a chance to appear, then confirm none did.
	time.Sleep(50 * time.Millisecond)
	if got := atomic.LoadInt64(&spawns); got != 3 {
		t.Fatalf("spawns = %d after settle, want 3 (drain must stop at zero backlog)", got)
	}
}

// TestDrainStopsWhenNoProgress: a run that does not reduce the backlog must NOT
// loop — the progress guard stops a comment the agent genuinely cannot advance
// from spinning forever. Exactly one run happens per human trigger.
func TestDrainStopsWhenNoProgress(t *testing.T) {
	var spawns int64
	e := New(Config{
		Enabled:    true,
		Debounce:   testDebounce,
		DrainDelay: time.Millisecond,
		Spawn: func(context.Context, string, string, string) error {
			atomic.AddInt64(&spawns, 1)
			return nil
		},
		// Backlog never shrinks → after >= before every run → no drain.
		PendingCount: func(string) (int, error) { return 5, nil },
	})
	e.Fire(webhook.EventCommentCreated, webhook.Event{})
	waitFor(t, func() bool { return atomic.LoadInt64(&spawns) >= 1 })
	time.Sleep(80 * time.Millisecond) // a drain loop, if any, would spawn more here
	if got := atomic.LoadInt64(&spawns); got != 1 {
		t.Fatalf("spawns = %d, want 1 (no progress ⇒ no drain loop)", got)
	}
}

// TestDrainDisabledWithoutCounter: with no PendingCount wired the drain is off —
// a single human trigger yields exactly one run (the pre-drain behaviour), even
// though a backlog conceptually remains.
func TestDrainDisabledWithoutCounter(t *testing.T) {
	var spawns int64
	e := New(Config{
		Enabled:    true,
		Debounce:   testDebounce,
		DrainDelay: time.Millisecond,
		Spawn: func(context.Context, string, string, string) error {
			atomic.AddInt64(&spawns, 1)
			return nil
		},
	})
	e.Fire(webhook.EventCommentCreated, webhook.Event{})
	waitFor(t, func() bool { return atomic.LoadInt64(&spawns) >= 1 })
	time.Sleep(80 * time.Millisecond)
	if got := atomic.LoadInt64(&spawns); got != 1 {
		t.Fatalf("spawns = %d, want 1 (drain disabled when PendingCount is nil)", got)
	}
}

// TestDrainPerProjectIsolation: a backlog on project A must not cause the engine
// to run project B's agent. Each project's runner drains its OWN queue.
func TestDrainPerProjectIsolation(t *testing.T) {
	var aRuns, bRuns int64
	var aRemaining int64 = 2
	e := New(Config{
		Enabled:    true,
		Debounce:   testDebounce,
		DrainDelay: time.Millisecond,
		Targets: map[string]Target{
			"alpha": {Root: "/repos/alpha", AgentCmd: "a {prompt}"},
			"beta":  {Root: "/repos/beta", AgentCmd: "b {prompt}"},
		},
		Spawn: func(_ context.Context, dir, _, _ string) error {
			if dir == "/repos/alpha" {
				atomic.AddInt64(&aRuns, 1)
				for {
					n := atomic.LoadInt64(&aRemaining)
					if n == 0 || atomic.CompareAndSwapInt64(&aRemaining, n, n-1) {
						return nil
					}
				}
			}
			atomic.AddInt64(&bRuns, 1)
			return nil
		},
		PendingCount: func(project string) (int, error) {
			if project == "alpha" {
				return int(atomic.LoadInt64(&aRemaining)), nil
			}
			return 0, nil
		},
	})
	e.Fire(webhook.EventCommentCreated, webhook.Event{Project: "alpha"})
	waitFor(t, func() bool { return atomic.LoadInt64(&aRemaining) == 0 })
	time.Sleep(50 * time.Millisecond)
	if got := atomic.LoadInt64(&aRuns); got != 2 {
		t.Fatalf("alpha runs = %d, want 2 (drained its own backlog)", got)
	}
	if got := atomic.LoadInt64(&bRuns); got != 0 {
		t.Fatalf("beta runs = %d, want 0 (a burst on alpha must not run beta)", got)
	}
}
