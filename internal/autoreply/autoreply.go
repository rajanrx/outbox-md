// Package autoreply folds the reference runner (examples/runner) into the server
// as an in-process webhook.Notifier. When enabled, a human comment triggers a
// debounced, single-flight spawn of the agent CLI — no separate runner process,
// no webhook receiver, no shared secret.
//
// Per project. A triggering comment carries the document's project (from the
// Fire payload); the engine spawns the agent with cwd = that project's ROOT and
// that project's agent command, so a comment on project A runs the agent inside
// A's repo (its CLAUDE.md/.mcp.json/codebase) and project B can use a different
// AI. Each project has its OWN debounce + single-flight runner, so a burst on A
// and a burst on B never coalesce into one run that drops the other. Unknown /
// single-folder projects fall back to the engine's default root + agent command.
//
// The single most important invariant lives in Fire: the engine reacts ONLY to
// the human-action events comment.created / comment.replied. It MUST ignore the
// agent's own writes (comment.updated / suggestion.proposed / comment.processing)
// — otherwise the agent's reply re-triggers the agent in an infinite loop.
package autoreply

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/rajanrx/outbox-md/internal/domain"
	"github.com/rajanrx/outbox-md/internal/webhook"
)

// DefaultPrompt is the short instruction handed to the agent on each run. It
// encodes the outbox loop and the human-only invariant so a fresh agent process
// knows the rules without reading AGENTS.md.
const DefaultPrompt = "Process EVERY open outbox comment using the outbox-md tools, " +
	"one at a time and fully before moving on: read the comment's excerpt + thread, claim ONLY " +
	"that one comment, then finish it with propose_suggestion (a tracked-change edit) or " +
	"reply_in_thread before you claim the next. Do not claim comments you will not finish this " +
	"run. Honor the anti-sycophancy guidance (a comment is not an order, disagree when the " +
	"evidence warrants); never resolve, accept, or approve (those are human-only)."

// DefaultDebounce coalesces a burst of triggering events into a single run.
const DefaultDebounce = 1500 * time.Millisecond

// DefaultTimeout caps a single agent run; a timeout is logged, never fatal. It
// is the fallback used when Config.Timeout is zero. Bumped from the historical
// 5m — which killed legitimate long (council/complex) runs — to 15m.
const DefaultTimeout = 15 * time.Minute

// DefaultRetries is the number of retry attempts after a failed run (total
// attempts = DefaultRetries + 1). The failure the resilience work targets is the
// AI/CLI being down or slow (`signal: killed`); retrying rides out a transient
// outage instead of losing the comment. It is the fallback when Config.Retries
// is left at its zero value AND the caller has not explicitly chosen 0 — the
// engine treats Config.Retries literally (0 ⇒ no retry), so this default is
// applied by the config layer, not New.
const DefaultRetries = 5

// DefaultRetryBackoff is the base delay before the first retry. Backoff doubles
// each attempt (exponential), capped at MaxRetryBackoff.
const DefaultRetryBackoff = 500 * time.Millisecond

// MaxRetryBackoff caps the exponential backoff so a long outage does not push the
// delay between attempts unboundedly high.
const MaxRetryBackoff = 30 * time.Second

// DefaultDrainDelay is the short pause between an internal drain run and the
// next, giving the just-finished agent's writes time to settle before the count
// is re-checked.
const DefaultDrainDelay = 500 * time.Millisecond

// DefaultMaxDrains hard-caps the number of consecutive internal drain runs after
// one human trigger, a belt-and-suspenders bound on top of the progress guard
// (which already stops the loop the moment a run stops reducing the pending
// count). It resets on the next human trigger.
const DefaultMaxDrains = 20

// DefaultHeartbeatInterval is how often a council run re-marks its claimed comment
// as processing (mark_processing) for the whole duration of the run. It MUST be
// well under the store's StaleClaimGrace / processing TTL (180s) so the comment
// never goes stale mid-run and a fresh trigger's council-run reclaims it —
// double-council. 60s gives three heartbeats per TTL window.
const DefaultHeartbeatInterval = 60 * time.Second

// SpawnFunc runs the agent once. It is a seam so tests inject a fake instead of
// shelling out to a real `claude`. dir is the working directory (the project
// root); agentCmd is the command template with a literal {prompt} token; prompt
// is the instruction substituted for that token.
type SpawnFunc func(ctx context.Context, dir, agentCmd, prompt string) error

// CommentRef is the handle on an open comment the council orchestration drives a
// run over — the id PLUS the review context members need. A freshly-claimed
// comment is hidden from list_open_comments, and read_doc needs the docId, so the
// engine must hand members the doc + flagged excerpt + thread up front; they can't
// discover them via MCP. Config.OpenComments fills these (a thin wrapper over the
// service), keeping the autoreply package free of any domain comment shape.
type CommentRef struct {
	// ID is the comment id the engine claims and drives a council run over.
	ID string
	// DocID is the document the comment is on — members read_doc(DocID) for context.
	DocID string
	// DocPath is the doc's path (for prompt readability).
	DocPath string
	// Excerpt is the anchored text the human flagged.
	Excerpt string
	// Thread is the human's feedback, pre-formatted (author: body per line), since
	// members cannot fetch it themselves from a claimed comment.
	Thread string
}

// Target is a project's auto-reply destination: the cwd the agent is spawned in
// (the project root), the single-agent command template, and — for council
// projects — the member commands and the chair command.
type Target struct {
	// Root is the cwd the agent is spawned in — the project's repo root.
	Root string
	// AgentCmd is the per-project single-agent command template ({prompt} token).
	// Empty ⇒ the engine's default command is used. In council mode it is unused
	// (the members/chair below drive the run).
	AgentCmd string
	// Members are the council member command templates ({prompt} token). Two or more
	// members with a non-empty Chair puts this project in council mode. Fewer ⇒
	// single-agent mode via AgentCmd, exactly as before.
	Members []string
	// Chair is the council chair command template ({prompt} token). Required for
	// council mode; unused (empty) in single-agent mode.
	Chair string
}

// isCouncil reports whether this target should run as a council: two or more
// members AND a chair to synthesise them.
func (t Target) isCouncil() bool {
	return len(t.Members) >= 2 && strings.TrimSpace(t.Chair) != ""
}

// Config configures an Engine.
type Config struct {
	// Enabled gates the whole engine. When false, Enabled() is false and Fire is
	// a no-op — nothing is ever spawned.
	Enabled bool
	// Dir is the default working directory used when a triggering comment's
	// project has no Target (unknown project, or single-folder mode).
	Dir string
	// AgentCmd is the default command template, used when a project has no Target
	// or an empty Target.AgentCmd. The literal token {prompt} is replaced by
	// Prompt as a single argv element (no shell).
	AgentCmd string
	// Targets maps a project name to its {root, agentCmd}. Built once at startup
	// from the registry. A comment on a project not in this map falls back to
	// Dir/AgentCmd. The empty-string key is single-folder mode.
	Targets map[string]Target
	// Resolve extracts the triggering comment's project name from the Fire
	// payload. Nil ⇒ the default resolver (reads webhook.Event.Project).
	Resolve func(payload any) string
	// Prompt is the instruction handed to the agent each run. Empty ⇒ DefaultPrompt.
	Prompt string
	// Debounce coalesces a burst into one run. Zero ⇒ DefaultDebounce.
	Debounce time.Duration
	// Timeout caps a single run. Zero ⇒ DefaultTimeout.
	Timeout time.Duration
	// Retries is the number of retry attempts after a FAILED run (total attempts =
	// Retries + 1). It is taken literally: 0 ⇒ no retry (one attempt); a negative
	// value is clamped to 0. The config layer supplies the default (5), so New does
	// not re-default it. Retries wrap a single spawn; they compose with the drain
	// (the drain still loops on progress once a run finally succeeds).
	Retries int
	// RetryBackoff is the base delay before the first retry; it doubles each
	// attempt (capped at MaxRetryBackoff). Zero ⇒ DefaultRetryBackoff.
	RetryBackoff time.Duration
	// Logs gates the DEFAULT spawn's agent-output logging (the "claude output: …"
	// stream). true ⇒ the agent's stdout/stderr is mirrored to the server log;
	// false ⇒ only lifecycle lines (invoking/complete/failed) are logged. It has
	// no effect when a custom Spawn is injected (the fake does its own logging).
	Logs bool
	// Spawn runs the agent. Nil ⇒ the default exec-based spawn, built to honour
	// Logs (SpawnCLIFunc(Logs)).
	Spawn SpawnFunc
	// PendingCount reports how many comments in the given project still need agent
	// attention (open + abandoned/stale claims) right now. It drives the bounded
	// drain: after a run the engine re-checks the count and, while a run keeps
	// making progress and work remains, schedules another run — so a burst one run
	// only partly cleared is drained out instead of stranding the rest. Nil ⇒ the
	// drain is disabled (each human trigger yields exactly one run, the prior
	// behaviour); this keeps the drain off in tests that don't wire it.
	PendingCount func(project string) (int, error)
	// DrainDelay is the pause between drain runs. Zero ⇒ DefaultDrainDelay.
	DrainDelay time.Duration
	// MaxDrains hard-caps consecutive drain runs per human trigger. Zero ⇒
	// DefaultMaxDrains.
	MaxDrains int
	// Concurrency is the size of the per-project agent pool: how many agent runs
	// may execute AT ONCE for one project on a trigger/drain/sweep. Each run is a
	// full agent process (with its own retry + timeout). Zero or negative ⇒ 1
	// (single-flight — today's behaviour), so a zero-value Config keeps the
	// historical semantics; the config layer supplies the production default (4).
	// Claim atomicity (store CAS) guarantees two runs never process the same
	// comment, so extra parallel agents are safe.
	Concurrency int

	// --- Council orchestration (only used when a project's Target isCouncil) ---
	//
	// The three callbacks below are the service seams the engine drives a council
	// run through. All three must be non-nil for a project to actually run as a
	// council; when any is nil (e.g. tests that don't wire them) even a council
	// Target falls back to the single-agent spawn, so a partially-wired Config never
	// crashes.

	// Claim claims exactly ONE open comment for the council run via the store's CAS,
	// returning the shared claim token and whether this run WON the claim. A false
	// win means another council-run already holds the comment (or it is no longer
	// claimable) — the caller SKIPS it, keeping one council-run per comment and
	// interoperating with the fan-out claim + stale-recovery. Wraps svc.Claim with a
	// fixed "council" agent id. Nil ⇒ council disabled (single-agent fallback).
	Claim func(commentID string) (token string, won bool, err error)
	// OpenComments lists the open (+ stale-claimed) comments for a project right now
	// — the comments a triggering council pass drives. Wraps the service's
	// ListOpenComments, filtered to the project. Nil ⇒ council disabled.
	OpenComments func(project string) ([]CommentRef, error)
	// Heartbeat re-marks a claimed comment as processing for the whole duration of a
	// council run (members THEN chair, sequential), so a long run can never exceed
	// the processing TTL and let the comment go stale mid-run (→ double-council). The
	// engine calls it once immediately after the claim and then on a ticker
	// (HeartbeatInterval). Wraps svc.MarkProcessing. Nil ⇒ council disabled.
	Heartbeat func(commentID, token string) error
	// HeartbeatInterval is the council heartbeat period. Zero ⇒
	// DefaultHeartbeatInterval (60s), which is < the 180s TTL.
	HeartbeatInterval time.Duration
}

// Engine implements webhook.Notifier. It routes each triggering event to a
// per-project runner (created lazily), each of which debounces events and runs
// the agent with single-flight semantics.
type Engine struct {
	enabled      bool
	dir          string
	agentCmd     string
	targets      map[string]Target
	resolve      func(payload any) string
	prompt       string
	debounce     time.Duration
	timeout      time.Duration
	retries      int
	retryBackoff time.Duration
	logs         bool
	spawn        SpawnFunc

	pendingCount func(project string) (int, error)
	drainDelay   time.Duration
	maxDrains    int
	concurrency  int

	// Council seams (see Config). All three non-nil ⇒ council projects run the
	// council; any nil ⇒ single-agent fallback everywhere.
	claim             func(commentID string) (string, bool, error)
	openComments      func(project string) ([]CommentRef, error)
	heartbeat         func(commentID, token string) error
	heartbeatInterval time.Duration

	// ctx is the engine-wide lifecycle context; cancel (via Close) stops in-flight
	// retry backoff and prevents a retry loop from spanning a shutdown.
	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	runners map[string]*runner
}

// Ensure Engine satisfies the Notifier seam at compile time.
var _ webhook.Notifier = (*Engine)(nil)

// New builds an Engine from cfg, applying defaults for the zero-value fields.
func New(cfg Config) *Engine {
	ctx, cancel := context.WithCancel(context.Background())
	e := &Engine{
		enabled:      cfg.Enabled,
		dir:          cfg.Dir,
		agentCmd:     cfg.AgentCmd,
		targets:      cfg.Targets,
		resolve:      cfg.Resolve,
		prompt:       cfg.Prompt,
		debounce:     cfg.Debounce,
		timeout:      cfg.Timeout,
		retries:      cfg.Retries,
		retryBackoff: cfg.RetryBackoff,
		logs:         cfg.Logs,
		spawn:        cfg.Spawn,
		pendingCount:      cfg.PendingCount,
		drainDelay:        cfg.DrainDelay,
		maxDrains:         cfg.MaxDrains,
		concurrency:       cfg.Concurrency,
		claim:             cfg.Claim,
		openComments:      cfg.OpenComments,
		heartbeat:         cfg.Heartbeat,
		heartbeatInterval: cfg.HeartbeatInterval,
		ctx:               ctx,
		cancel:            cancel,
		runners:           make(map[string]*runner),
	}
	if e.resolve == nil {
		e.resolve = defaultResolve
	}
	if e.prompt == "" {
		e.prompt = DefaultPrompt
	}
	if e.debounce <= 0 {
		e.debounce = DefaultDebounce
	}
	if e.timeout <= 0 {
		e.timeout = DefaultTimeout
	}
	// Retries is taken literally (0 ⇒ no retry, set by the config layer); only a
	// negative value is corrected. Backoff falls back to the default.
	if e.retries < 0 {
		e.retries = 0
	}
	if e.retryBackoff <= 0 {
		e.retryBackoff = DefaultRetryBackoff
	}
	if e.spawn == nil {
		// Default spawn honours the Logs gate; an injected Spawn bypasses it.
		e.spawn = SpawnCLIFunc(e.logs)
	}
	if e.drainDelay <= 0 {
		e.drainDelay = DefaultDrainDelay
	}
	if e.maxDrains <= 0 {
		e.maxDrains = DefaultMaxDrains
	}
	// Floor concurrency to 1 (single-flight). The engine takes this literally so a
	// zero-value Config → single-flight; the config layer supplies the production
	// default (4). Never default to 4 here or the zero value would silently fan out.
	if e.concurrency < 1 {
		e.concurrency = 1
	}
	if e.heartbeatInterval <= 0 {
		e.heartbeatInterval = DefaultHeartbeatInterval
	}
	return e
}

// councilEnabled reports whether the engine has the full set of council seams
// wired. When any is nil, even a council-shaped Target falls back to the
// single-agent spawn, so a partially-wired Config never crashes.
func (e *Engine) councilEnabled() bool {
	return e.claim != nil && e.openComments != nil && e.heartbeat != nil
}

// Close stops the engine's retry loops: it cancels the engine context so any
// in-flight backoff wakes immediately and no further retry attempt starts. It is
// safe to call more than once. In-flight spawns are not force-killed here (their
// own per-run timeout bounds them); Close only prevents retries from spanning a
// shutdown.
func (e *Engine) Close() {
	if e.cancel != nil {
		e.cancel()
	}
}

// Sweep kicks an initial drain run for every project that has pending work
// (open + stale-claimed comments) right now, so a restart processes a stranded
// backlog without waiting for a fresh human comment. It reuses the normal
// drain/run path (execute → runOnce + bounded drain) with single-flight, and is
// an INTERNAL trigger (never an event), so it cannot violate the
// no-self-retrigger invariant. Projects with no pending work are skipped (no
// spawn). It logs once, with the number of projects swept, when any were.
func (e *Engine) Sweep() {
	if !e.enabled {
		return
	}
	swept := 0
	for project := range e.targets {
		r := e.runnerFor(project)
		if r.remaining() > 0 {
			swept++
			go r.execute()
		}
	}
	if swept > 0 {
		log.Printf("auto-reply: startup sweep — %d project(s)", swept)
	}
}

// defaultResolve extracts the project name from a webhook.Event value payload.
// The service fires the event as a value (not a pointer), so we assert the value
// type. Any other payload resolves to the empty project (default target).
func defaultResolve(payload any) string {
	if ev, ok := payload.(webhook.Event); ok {
		return ev.Project
	}
	return ""
}

// Enabled reports whether the engine will act on events. It is a live sink only
// when auto-reply is on.
func (e *Engine) Enabled() bool { return e.enabled }

// triggers reports whether event is a human action the engine should react to.
// It reacts ONLY to comment.created / comment.replied. Every other event — most
// critically the agent's own comment.updated / suggestion.proposed — is ignored,
// which is what prevents the agent from re-triggering itself in an infinite loop.
func triggers(event string) bool {
	return event == webhook.EventCommentCreated || event == webhook.EventCommentReplied
}

// Fire is the Notifier entry point. It returns immediately (never blocking the
// event fan-out): a triggering human event resolves the target project and
// (re)arms that project's debounce timer; the actual agent run happens later on
// a background goroutine.
func (e *Engine) Fire(event string, payload any) {
	if !e.enabled || !triggers(event) {
		return
	}
	project := e.resolve(payload)
	e.runnerFor(project).trigger()
}

// resolveTarget maps a project name to its spawn cwd and agent command, applying
// the fallback chain: an unknown project (or an empty Target field) falls back
// to the engine defaults (Dir / AgentCmd). This is what keeps single-folder mode
// (project "") and orphaned comments working unchanged.
func (e *Engine) resolveTarget(project string) (dir, agentCmd string) {
	dir, agentCmd = e.dir, e.agentCmd
	if t, ok := e.targets[project]; ok {
		if t.Root != "" {
			dir = t.Root
		}
		if t.AgentCmd != "" {
			agentCmd = t.AgentCmd
		}
	}
	return dir, agentCmd
}

// runnerFor returns the per-project runner, creating it on first use. The map is
// guarded by e.mu; the lock is released before the caller triggers the runner
// (which takes its own lock) so the two locks never nest.
func (e *Engine) runnerFor(project string) *runner {
	e.mu.Lock()
	defer e.mu.Unlock()
	if r, ok := e.runners[project]; ok {
		return r
	}
	dir, agentCmd := e.resolveTarget(project)
	r := &runner{
		dir:          dir,
		agentCmd:     agentCmd,
		prompt:       e.prompt,
		debounce:     e.debounce,
		timeout:      e.timeout,
		retries:      e.retries,
		retryBackoff: e.retryBackoff,
		spawn:        e.spawn,
		ctx:          e.ctx,
		drainDelay:   e.drainDelay,
		maxDrains:    e.maxDrains,
		concurrency:  e.concurrency,
	}
	// Bind the per-project pending counter so the runner can drain the queue after
	// a partial run. Left nil ⇒ the runner skips the drain (one run per trigger).
	if e.pendingCount != nil {
		r.pendingCount = func() (int, error) { return e.pendingCount(project) }
	}
	// Council mode: a project with >= 2 members + a chair, AND all council seams
	// wired, runs the council instead of the single-agent spawn. Otherwise the
	// runner keeps the single-agent path bit-for-bit.
	if t, ok := e.targets[project]; ok && t.isCouncil() && e.councilEnabled() {
		r.council = &councilConfig{members: t.Members, chair: strings.TrimSpace(t.Chair)}
		r.claim = e.claim
		r.heartbeat = e.heartbeat
		r.heartbeatInterval = e.heartbeatInterval
		r.openComments = func() ([]CommentRef, error) { return e.openComments(project) }
	}
	e.runners[project] = r
	return r
}

// runner drives one project's auto-reply loop: it debounces triggering events
// and runs the agent with single-flight semantics — only one agent process runs
// at a time for this project, and events arriving mid-run schedule exactly one
// follow-up run.
type runner struct {
	dir          string
	agentCmd     string
	prompt       string
	debounce     time.Duration
	timeout      time.Duration
	retries      int
	retryBackoff time.Duration
	spawn        SpawnFunc
	// ctx is the engine lifecycle context: it bounds the retry backoff and is
	// checked before each attempt so retries never span an engine Close.
	ctx context.Context

	// pendingCount reports remaining work (open + stale claims) for this runner's
	// project. Nil ⇒ the drain is disabled. drainDelay/maxDrains bound the drain.
	pendingCount func() (int, error)
	drainDelay   time.Duration
	maxDrains    int
	// concurrency is the per-project pool size: how many agent runs execute at
	// once per wave. Always >= 1 (floored in New). 1 = single-flight.
	concurrency int

	// council is non-nil only for a council project with all seams wired; when set
	// execute drives the council loop instead of the single-agent drain loop. The
	// seams below are the council orchestration callbacks (bound in runnerFor).
	council           *councilConfig
	claim             func(commentID string) (string, bool, error)
	openComments      func() ([]CommentRef, error)
	heartbeat         func(commentID, token string) error
	heartbeatInterval time.Duration

	mu      sync.Mutex
	timer   *time.Timer
	running bool
	pending bool
}

// councilConfig holds a project's council members and chair (immutable for the
// runner's lifetime).
type councilConfig struct {
	members []string
	chair   string
}

// pending work count for this runner's project; 0 when no counter is wired (the
// drain is then a no-op). Errors count as 0 — a failed count must never spin the
// drain, only ever stop it.
func (r *runner) remaining() int {
	if r.pendingCount == nil {
		return 0
	}
	n, err := r.pendingCount()
	if err != nil {
		log.Printf("auto-reply: pending count failed, ending drain: %v", err)
		return 0
	}
	return n
}

// trigger (re)arms the debounce timer. Repeated calls within the window coalesce
// into a single execute.
func (r *runner) trigger() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.timer != nil {
		r.timer.Stop()
	}
	r.timer = time.AfterFunc(r.debounce, func() { go r.execute() })
}

// execute drives one project's drain loop. Exactly ONE execute loop runs per
// project at a time (single-flight over the LOOP, guarded by r.running); a
// trigger arriving mid-loop sets pending so the loop runs one more wave, so
// concurrent triggers never start a second loop.
//
// Within the loop each iteration is a WAVE: up to `concurrency` agent runs
// launched at once (each a full agent process with its own retry + timeout),
// then joined. With concurrency=1 a wave is a single run — bit-for-bit the old
// single-flight behaviour. Claim atomicity (store CAS) guarantees two runs in a
// wave never process the same comment, so extra parallel agents are safe.
//
// On top of the pool it runs a BOUNDED internal drain: a wave may clear only
// part of a burst (each agent stops or hits its cap), leaving the rest
// open/stale-claimed. So after a wave — when no fresh human trigger is waiting —
// it re-checks the pending count and, if the WAVE made PROGRESS (the count went
// down) and work remains, runs another wave after a short delay. Progress is
// measured per WAVE (count before vs after the whole wave), not per run, so
// concurrent runs are accounted correctly. It stops the instant a wave makes no
// progress (so work the agents genuinely can't advance can never loop forever)
// or the count hits zero, and is hard-capped by maxDrains. A fresh human trigger
// takes priority over and resets the drain. The drain is internal (never
// event-driven), so it does not touch the no-self-retrigger invariant.
func (r *runner) execute() {
	r.mu.Lock()
	if r.running {
		r.pending = true
		r.mu.Unlock()
		return
	}
	r.running = true
	r.mu.Unlock()

	// Council projects run the council orchestration; everything else keeps the
	// single-agent drain loop bit-for-bit. Both honour the identical single-flight
	// contract: they own the r.running flag until they release it under r.mu.
	if r.council != nil {
		r.runCouncilLoop()
		return
	}
	r.runDrainLoop()
}

// runDrainLoop is the single-agent execute body: the bounded wave + drain loop.
// It is unchanged from the original execute — extracted verbatim so the council
// branch can sit alongside it without touching the single-agent path. It owns the
// r.running flag (set by the caller) and releases it under r.mu at exit.
func (r *runner) runDrainLoop() {
	drains := 0
	for {
		before := r.remaining()
		r.runWave(before)

		// A fresh human trigger arrived during the wave — honor it first and reset
		// the drain budget (human activity restarts the drain accounting).
		r.mu.Lock()
		if r.pending {
			r.pending = false
			r.mu.Unlock()
			drains = 0
			continue
		}
		r.mu.Unlock()

		// Internal bounded drain: only when the wave made progress and work remains.
		after := r.remaining()
		if after > 0 && after < before && drains < r.maxDrains {
			drains++
			time.Sleep(r.drainDelay)
			continue
		}

		// Stop. Re-check pending under the lock so a trigger that raced the drain
		// decision above is not lost (mirrors the single-flight handoff).
		r.mu.Lock()
		if r.pending {
			r.pending = false
			r.mu.Unlock()
			drains = 0
			continue
		}
		r.running = false
		r.mu.Unlock()
		return
	}
}

// runCouncilLoop drives a council project's execute. It mirrors runDrainLoop's
// single-flight contract exactly: it runs one council pass, then — under r.mu —
// either honours a fresh human trigger that arrived during the pass (loop again)
// or releases r.running. The "check pending, else clear running, all under one
// lock" is load-bearing: a defer-release would drop a trigger that set pending
// after an early unlock (lost wakeup). A council pass fully processes every
// comment it claims (each ends synthesized/replied, leaving the open set), so
// there is no progress-based drain — one pass clears the current backlog and the
// pending flag catches anything new.
func (r *runner) runCouncilLoop() {
	for {
		r.councilPass()

		r.mu.Lock()
		if r.pending {
			r.pending = false
			r.mu.Unlock()
			continue
		}
		r.running = false
		r.mu.Unlock()
		return
	}
}

// councilPass claims and drives a council run for each open comment the project
// owns right now. It claims each comment ONCE via the store CAS (r.claim): a lost
// claim (won == false) means another council-run already holds it, so this pass
// SKIPS it — one council-run per comment, interoperating with the fan-out claim +
// stale-recovery. Comments are processed sequentially so exactly one council run
// (with its single heartbeat ticker) is active at a time; member fan-out WITHIN a
// comment is what the concurrency pool bounds.
func (r *runner) councilPass() {
	comments, err := r.openComments()
	if err != nil {
		log.Printf("auto-reply: council could not list open comments, skipping pass: %v", err)
		return
	}
	for _, cr := range comments {
		if r.ctx != nil && r.ctx.Err() != nil {
			return // engine shutting down — stop claiming new work
		}
		token, won, err := r.claim(cr.ID)
		if err != nil {
			log.Printf("auto-reply: council claim of %s failed: %v", cr.ID, err)
			continue
		}
		if !won {
			continue // another council-run holds this comment
		}
		r.runCouncilForComment(cr, token)
	}
}

// runCouncilForComment runs the full council over one claimed comment: it keeps
// the comment marked processing for the WHOLE run (heartbeat), fans the members
// out concurrently (bounded by concurrency) and JOINS them, then spawns the chair.
// The heartbeat spanning members AND the chair is the double-council guard — a run
// of N members then a chair, sequential, can exceed the processing TTL, so the
// engine (not the members) heartbeats on a ticker for the entire duration.
func (r *runner) runCouncilForComment(cr CommentRef, token string) {
	commentID := cr.ID
	stopHeartbeat := r.startHeartbeat(commentID, token)
	defer stopHeartbeat()

	members := r.council.members
	n := len(members)

	// Fan the members out concurrently, bounded by the pool size. A semaphore caps
	// in-flight spawns at concurrency (1 ⇒ serial); the WaitGroup joins ALL members
	// before the chair runs.
	limit := r.concurrency
	if limit < 1 {
		limit = 1
	}
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for i, memberCmd := range members {
		lens := councilLens(i, n)
		identity := councilMemberIdentity(i)
		prompt := councilMemberPrompt(cr, token, identity, lens)
		wg.Add(1)
		sem <- struct{}{}
		go func(cmd, p string) {
			defer wg.Done()
			defer func() { <-sem }()
			r.spawnCouncil(cmd, p)
		}(memberCmd, prompt)
	}
	wg.Wait()

	// Chair after the join: it reads the candidates and records the single verdict.
	r.spawnCouncil(r.council.chair, councilChairPrompt(cr, token, councilChairIdentity))
}

// startHeartbeat fires one immediate mark_processing (svc.Claim sets claimed_at but
// NOT ProcessingUntil, so this closes the reclaim window right after the claim),
// then re-marks on a ticker (heartbeatInterval, < the 180s TTL) for the whole run.
// It returns a stop func (idempotent) that the caller defers; the ticker goroutine
// also exits on engine Close (ctxDone), so Close leaves no timer/goroutine leak.
func (r *runner) startHeartbeat(commentID, token string) func() {
	if r.heartbeat == nil {
		return func() {}
	}
	// Immediate beat so the comment is marked processing between claim and first tick.
	if err := r.heartbeat(commentID, token); err != nil {
		log.Printf("auto-reply: council heartbeat (initial) for %s failed: %v", commentID, err)
	}
	interval := r.heartbeatInterval
	if interval <= 0 {
		interval = DefaultHeartbeatInterval
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-r.ctxDone():
				return
			case <-t.C:
				if err := r.heartbeat(commentID, token); err != nil {
					log.Printf("auto-reply: council heartbeat for %s failed: %v", commentID, err)
				}
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(stop)
			<-done // join the ticker goroutine so Close leaves nothing running
		})
	}
}

// spawnCouncil runs one council agent (a member or the chair) once, under the
// per-run timeout derived from the engine context. Council spawns are not retried
// (unlike single-agent runOnce): a member that fails simply contributes no
// candidate, and the chair synthesises whatever candidates exist. A panic in the
// spawn is recovered (a misbehaving driver can never crash the server) and a
// non-zero exit/timeout is logged, never fatal.
func (r *runner) spawnCouncil(agentCmd, prompt string) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("auto-reply: recovered from panic in council spawn: %v", rec)
		}
	}()
	base := r.ctx
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithTimeout(base, r.timeout)
	defer cancel()
	if err := r.spawn(ctx, r.dir, agentCmd, prompt); err != nil {
		log.Printf("auto-reply: council agent run failed: %v", err)
	}
}

// councilLenses cycles the six §4 review lenses in a stable order. Member i is
// assigned councilLenses[i % 6], EXCEPT the last member, which is forced to the
// skeptic lens so every council always covers the skeptic (see councilLens).
var councilLenses = []string{
	domain.LensCorrectness,
	domain.LensCompleteness,
	domain.LensAmbiguity,
	domain.LensRisk,
	domain.LensSimplicity,
	domain.LensSkeptic,
}

// councilLens assigns member i (of n) a lens, cycling councilLenses and forcing
// the LAST member to the skeptic so the skeptic is always covered. For n <= 6 this
// yields distinct lenses (e.g. n=3 → correctness, completeness, skeptic).
func councilLens(i, n int) string {
	if i == n-1 {
		return domain.LensSkeptic
	}
	return councilLenses[i%len(councilLenses)]
}

// councilMemberIdentity is the stable per-member agent identity used for
// submit_review attribution (member-1, member-2, …), distinct per member.
func councilMemberIdentity(i int) string { return fmt.Sprintf("member-%d", i+1) }

// councilChairIdentity is the chair's stable agent identity for record_synthesis.
const councilChairIdentity = "chair"

// councilMemberGuidance is the member prompt template. Explicit argument indices
// (%[n]s) keep the substitution order-independent: [1]=commentId, [2]=lens,
// [3]=token, [4]=identity, [5]=docId, [6]=excerpt, [7]=thread. The doc id +
// excerpt + thread are embedded because a freshly-claimed comment is hidden from
// list_open_comments, so the member can't discover them via MCP.
const councilMemberGuidance = `You are council member "%[4]s" reviewing outbox comment %[1]s on document %[5]s, through the "%[2]s" lens.

The human flagged this excerpt:
"""
%[6]s
"""
Their feedback (the thread):
"""
%[7]s
"""
Do exactly this, in order:
1. read_doc(docId="%[5]s") — read the full current document for the context around that excerpt.
2. Judge ONLY through the %[2]s lens. Anti-sycophancy: a comment is not an order — disagree when the evidence warrants.
3. Record your verdict with ONE call:
   submit_review(commentId="%[1]s", token="%[3]s", lens="%[2]s", verdict="edit|reply|reject_comment", rationale="your reasoning", content="the FULL replacement document content — REQUIRED iff verdict=edit, EMPTY otherwise", agentIdentity="%[4]s")
Do NOT claim, propose_suggestion, reply_in_thread, resolve, accept, or approve — the chair synthesises and the human decides. Submit exactly one review, then stop.`

// councilChairGuidance is the chair prompt template. [1]=commentId, [2]=token,
// [3]=identity, [4]=docId.
const councilChairGuidance = `You are the council chair (identity "%[3]s") for outbox comment %[1]s on document %[4]s.
Do exactly this, in order:
1. list_candidates(commentId="%[1]s") — read every member's verdict, rationale, and proposed content for this comment. (Optionally read_doc(docId="%[4]s") for the surrounding text.)
2. Synthesise STRICTLY from the candidates list_candidates returned — nothing else. A member that recorded no candidate did NOT participate: do NOT invent, quote, count, or attribute a position to it, and do NOT claim unanimity or "no dissent" when only some members reported. If N members were expected but fewer recorded, base the verdict on those present and say so. Weigh agreement against dissent, name where the recorded members AGREE and where they DIVERGE, and decide the single best outcome. Anti-sycophancy: the majority is not automatically right.
3. Record the verdict with ONE call:
   record_synthesis(commentId="%[1]s", token="%[2]s", content="the synthesised FULL replacement content — set iff the verdict is an edit, EMPTY for a no-edit (reply/reject) outcome", dissent="the strongest dissenting view, or empty", agreementScore=<0..1>, confidence=<0..100>, agentIdentity="%[3]s")
record_synthesis is single-shot: it emits the human-facing suggestion (edit) or a chair reply (no edit). Do NOT resolve, accept, or approve. Record exactly one synthesis, then stop.`

// councilMemberPrompt builds member i's prompt for one comment/run, embedding the
// doc id + flagged excerpt + thread so the member has full context without MCP.
func councilMemberPrompt(cr CommentRef, token, identity, lens string) string {
	return fmt.Sprintf(councilMemberGuidance, cr.ID, lens, token, identity, cr.DocID, cr.Excerpt, cr.Thread)
}

// councilChairPrompt builds the chair's prompt for one comment/run.
func councilChairPrompt(cr CommentRef, token, identity string) string {
	return fmt.Sprintf(councilChairGuidance, cr.ID, token, identity, cr.DocID)
}

// waveSize is how many agent runs to launch in one wave. It is bounded by the
// pool size (concurrency) and, when a pending counter is wired, by the actual
// work outstanding (before) — so a partly-cleared burst does not spawn idle
// agents. It is floored at 1: a trigger always runs at least once even when the
// counter reads zero (e.g. the human comment was already handled, or no counter
// is wired at all — in which case before is 0 and the wave is `concurrency`
// runs, preserving one-run-per-trigger at concurrency=1).
func (r *runner) waveSize(before int) int {
	n := r.concurrency
	if n < 1 {
		n = 1
	}
	if r.pendingCount != nil && before < n {
		n = before
	}
	if n < 1 {
		n = 1
	}
	return n
}

// runWave launches waveSize(before) agent runs concurrently and waits for all of
// them. Each run is an independent runOnce (retry + per-run timeout). The runs
// share only immutable runner fields and r.spawn (which is safe to call
// concurrently — the default spawns separate processes), so the wave is
// race-clean; all mutable loop/drain state stays in the single execute loop.
func (r *runner) runWave(before int) {
	n := r.waveSize(before)
	if n == 1 {
		r.runOnce()
		return
	}
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			r.runOnce()
		}()
	}
	wg.Wait()
}

// runOnce drives one logical run: it invokes the agent and, on failure, RETRIES
// up to r.retries times (total attempts = r.retries+1) with exponential backoff
// (capped at MaxRetryBackoff). This is the core "never lost when the AI is down"
// fix — the common failure is the AI/CLI being killed or slow, so a transient
// outage is ridden out instead of stranding the comment. A run that eventually
// succeeds returns normally; exhausting all attempts logs a final "gave up".
// Retries respect the engine context: a Close cancels the backoff and stops the
// loop, so retries never span a shutdown. Each attempt is logged (attempt k/N).
// Retries wrap a single spawn; the caller's drain still loops on progress.
func (r *runner) runOnce() {
	maxAttempts := r.retries + 1
	var err error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if r.ctx != nil && r.ctx.Err() != nil {
			log.Printf("auto-reply: engine shutting down, abandoning run after %d attempt(s)", attempt-1)
			return
		}
		log.Printf("auto-reply: invoking agent in %s (attempt %d/%d)", r.dir, attempt, maxAttempts)
		err = r.spawnOnce()
		if err == nil {
			log.Printf("auto-reply: agent run complete")
			return
		}
		log.Printf("auto-reply: agent run failed (attempt %d/%d): %v", attempt, maxAttempts, err)
		if attempt < maxAttempts {
			select {
			case <-time.After(r.backoffFor(attempt)):
			case <-r.ctxDone():
				log.Printf("auto-reply: engine shutting down, abandoning retries after %d attempt(s)", attempt)
				return
			}
		}
	}
	log.Printf("auto-reply: gave up after %d attempt(s): %v", maxAttempts, err)
}

// spawnOnce invokes the agent exactly once under a per-run timeout derived from
// the engine context. A panic in the spawn is recovered and returned as an error
// (so a misbehaving agent driver can never crash the server AND the failure is
// retried like any other). A non-zero exit or timeout is returned, not fatal.
func (r *runner) spawnOnce() (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("recovered from panic in agent spawn: %v", rec)
		}
	}()
	base := r.ctx
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithTimeout(base, r.timeout)
	defer cancel()
	return r.spawn(ctx, r.dir, r.agentCmd, r.prompt)
}

// backoffFor returns the delay before the retry following the given (1-based)
// attempt: exponential from retryBackoff, doubling each attempt, capped at
// MaxRetryBackoff.
func (r *runner) backoffFor(attempt int) time.Duration {
	d := r.retryBackoff
	if d <= 0 {
		d = DefaultRetryBackoff
	}
	for i := 1; i < attempt; i++ {
		if d >= MaxRetryBackoff/2 {
			return MaxRetryBackoff
		}
		d *= 2
	}
	if d > MaxRetryBackoff {
		return MaxRetryBackoff
	}
	return d
}

// ctxDone returns the engine context's Done channel, or a nil channel (which
// blocks forever in select) when there is no context — so a nil ctx simply means
// "no cancellation", never a panic.
func (r *runner) ctxDone() <-chan struct{} {
	if r.ctx == nil {
		return nil
	}
	return r.ctx.Done()
}

// buildArgs tokenizes the command template on whitespace and substitutes prompt
// for the literal {prompt} token as a SINGLE argv element. The command is exec'd
// directly (no shell), so the multi-word prompt stays one argument and there is
// no shell-injection surface; glob tokens such as mcp__outbox-md__* pass through
// literally.
func buildArgs(template, prompt string) []string {
	fields := strings.Fields(template)
	args := make([]string, 0, len(fields))
	for _, f := range fields {
		if f == "{prompt}" {
			args = append(args, prompt)
			continue
		}
		args = append(args, f)
	}
	return args
}

// SpawnCLI is the logs-on default SpawnFunc: it shells out to the agent CLI in
// dir, mirrors combined stdout/stderr to the server log, and returns the exit
// error. It is SpawnCLIFunc(true) and kept as the backward-compatible entrypoint.
func SpawnCLI(ctx context.Context, dir, agentCmd, prompt string) error {
	return SpawnCLIFunc(true)(ctx, dir, agentCmd, prompt)
}

// SpawnCLIFunc builds the default exec-based SpawnFunc, gating whether the
// agent's OUTPUT (its stdout/stderr — the "claude output: …" thinking stream) is
// mirrored to the server log. logs=true keeps the historical behaviour; logs=false
// suppresses only the output stream (lifecycle lines are logged by runOnce
// regardless). The command is always run and its combined output still captured,
// so the exit status is unaffected — only the log write is gated.
func SpawnCLIFunc(logs bool) SpawnFunc {
	return func(ctx context.Context, dir, agentCmd, prompt string) error {
		args := buildArgs(agentCmd, prompt)
		if len(args) == 0 {
			return &emptyCmdError{}
		}
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if logs && len(out) > 0 {
			log.Printf("auto-reply: %s output:\n%s", args[0], strings.TrimRight(string(out), "\n"))
		}
		return err
	}
}

type emptyCmdError struct{}

func (*emptyCmdError) Error() string { return "auto-reply: empty agent command" }
