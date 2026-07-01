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
