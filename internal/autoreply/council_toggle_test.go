package autoreply

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rajanrx/outbox-md/internal/webhook"
)

// spawnRec captures every spawn's command + prompt so a test can assert the exact
// shape of the council-OFF single-agent path vs the full council.
type spawnRec struct {
	mu    sync.Mutex
	cmds  []string
	promp []string
}

func (r *spawnRec) spawn(_ context.Context, _, agentCmd, prompt string) error {
	r.mu.Lock()
	r.cmds = append(r.cmds, agentCmd)
	r.promp = append(r.promp, prompt)
	r.mu.Unlock()
	return nil
}

func (r *spawnRec) snapshot() ([]string, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.cmds...), append([]string(nil), r.promp...)
}

// toggleConfig builds an Enabled council engine (2 members + chair, all seams
// wired) whose single open comment carries the given CouncilEnabled flag, served
// once. hbCalls counts heartbeats.
func toggleConfig(rec *spawnRec, councilOn bool, hbCalls *int32) Config {
	var served int32
	return Config{
		Enabled:     true,
		Debounce:    testDebounce,
		Concurrency: 2,
		Resolve:     func(any) string { return "p" },
		Targets: map[string]Target{
			"p": {Root: "/p", Members: []string{"member-cmd-a", "member-cmd-b"}, Chair: "chair-cmd"},
		},
		Spawn: rec.spawn,
		Claim: func(string) (string, bool, error) { return "tok-123", true, nil },
		OpenComments: func(string) ([]CommentRef, error) {
			if atomic.AddInt32(&served, 1) > 1 {
				return nil, nil
			}
			return []CommentRef{{ID: "c1", DocID: "doc-1", Excerpt: "flagged", Thread: "human: fix", CouncilEnabled: councilOn}}, nil
		},
		Heartbeat: func(string, string) error {
			if hbCalls != nil {
				atomic.AddInt32(hbCalls, 1)
			}
			return nil
		},
	}
}

// TestCouncilOffRunsSingleAgent is the core per-comment toggle test: on a COUNCIL
// project, a comment with CouncilEnabled=false is processed SINGLE-AGENT — exactly
// one spawn, of the CHAIR command, with a single-agent prompt (propose_suggestion /
// reply_in_thread + the claim token) and NO member fan-out / council MCP roles. The
// comment is still claimed + heartbeated for the run.
func TestCouncilOffRunsSingleAgent(t *testing.T) {
	rec := &spawnRec{}
	var hb int32
	e := New(toggleConfig(rec, false, &hb))
	defer e.Close()

	e.Fire(webhook.EventCommentCreated, nil)
	waitFor(t, func() bool {
		cmds, _ := rec.snapshot()
		return len(cmds) >= 1
	})
	// Settle: no further spawns (no member wave, no separate chair synthesis).
	time.Sleep(60 * time.Millisecond)

	cmds, prompts := rec.snapshot()
	if len(cmds) != 1 {
		t.Fatalf("council-off spawned %d agents, want 1 (single-agent): %v", len(cmds), cmds)
	}
	if cmds[0] != "chair-cmd" {
		t.Fatalf("council-off used command %q, want chair-cmd", cmds[0])
	}
	// No member commands were spawned.
	for _, c := range cmds {
		if strings.HasPrefix(c, "member-cmd") {
			t.Fatalf("council-off fanned out a member (%q) — must not", c)
		}
	}
	p := prompts[0]
	for _, want := range []string{"propose_suggestion", "reply_in_thread", "tok-123", "doc-1", "flagged"} {
		if !strings.Contains(p, want) {
			t.Fatalf("council-off prompt missing %q:\n%s", want, p)
		}
	}
	for _, unwanted := range []string{"record_synthesis", "submit_review", "submit_discussion"} {
		if strings.Contains(p, unwanted) {
			t.Fatalf("council-off prompt uses council-only tool %q (must be plain single-agent):\n%s", unwanted, p)
		}
	}
	// The comment is still claimed + heartbeated during the single-agent run.
	if atomic.LoadInt32(&hb) < 1 {
		t.Fatalf("council-off run heartbeated %d times, want >= 1", atomic.LoadInt32(&hb))
	}
}

// TestCouncilOnRunsCouncil is the paired assertion: the SAME council project, with
// the comment's flag ON, runs the full council — the two members fan out (member
// commands) and the chair records a synthesis (record_synthesis prompt).
func TestCouncilOnRunsCouncil(t *testing.T) {
	rec := &spawnRec{}
	e := New(toggleConfig(rec, true, nil))
	defer e.Close()

	e.Fire(webhook.EventCommentCreated, nil)
	waitFor(t, func() bool {
		cmds, _ := rec.snapshot()
		// members (2) + chair (1)
		return len(cmds) >= 3
	})
	time.Sleep(40 * time.Millisecond)

	cmds, prompts := rec.snapshot()
	members, chairs := 0, 0
	chairPrompt := ""
	for i, c := range cmds {
		switch {
		case strings.HasPrefix(c, "member-cmd"):
			members++
		case c == "chair-cmd":
			chairs++
			chairPrompt = prompts[i]
		}
	}
	if members != 2 || chairs != 1 {
		t.Fatalf("council-on spawned members=%d chair=%d, want 2/1", members, chairs)
	}
	if !strings.Contains(chairPrompt, "record_synthesis") {
		t.Fatalf("council-on chair prompt missing record_synthesis (not the chair role):\n%s", chairPrompt)
	}
}
