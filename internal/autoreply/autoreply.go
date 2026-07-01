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
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/rajanrx/outbox-md/internal/webhook"
)

// DefaultPrompt is the short instruction handed to the agent on each run. It
// encodes the outbox loop and the human-only invariant so a fresh agent process
// knows the rules without reading AGENTS.md.
const DefaultPrompt = "Process the open outbox comments using the outbox-md tools — " +
	"read each comment's excerpt + thread, claim it, then propose_suggestion (a tracked-change " +
	"edit) or reply_in_thread; honor the anti-sycophancy guidance (a comment is not an order, " +
	"disagree when the evidence warrants); never resolve, accept, or approve (those are human-only)."

// DefaultDebounce coalesces a burst of triggering events into a single run.
const DefaultDebounce = 1500 * time.Millisecond

// DefaultTimeout caps a single agent run; a timeout is logged, never fatal.
const DefaultTimeout = 5 * time.Minute

// SpawnFunc runs the agent once. It is a seam so tests inject a fake instead of
// shelling out to a real `claude`. dir is the working directory (the project
// root); agentCmd is the command template with a literal {prompt} token; prompt
// is the instruction substituted for that token.
type SpawnFunc func(ctx context.Context, dir, agentCmd, prompt string) error

// Target is a project's auto-reply destination: the cwd the agent is spawned in
// (the project root) and the agent command template for that project.
type Target struct {
	// Root is the cwd the agent is spawned in — the project's repo root.
	Root string
	// AgentCmd is the per-project agent command template ({prompt} token). Empty
	// ⇒ the engine's default command is used.
	AgentCmd string
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
	// Spawn runs the agent. Nil ⇒ the default exec-based spawn (SpawnCLI).
	Spawn SpawnFunc
}

// Engine implements webhook.Notifier. It routes each triggering event to a
// per-project runner (created lazily), each of which debounces events and runs
// the agent with single-flight semantics.
type Engine struct {
	enabled  bool
	dir      string
	agentCmd string
	targets  map[string]Target
	resolve  func(payload any) string
	prompt   string
	debounce time.Duration
	timeout  time.Duration
	spawn    SpawnFunc

	mu      sync.Mutex
	runners map[string]*runner
}

// Ensure Engine satisfies the Notifier seam at compile time.
var _ webhook.Notifier = (*Engine)(nil)

// New builds an Engine from cfg, applying defaults for the zero-value fields.
func New(cfg Config) *Engine {
	e := &Engine{
		enabled:  cfg.Enabled,
		dir:      cfg.Dir,
		agentCmd: cfg.AgentCmd,
		targets:  cfg.Targets,
		resolve:  cfg.Resolve,
		prompt:   cfg.Prompt,
		debounce: cfg.Debounce,
		timeout:  cfg.Timeout,
		spawn:    cfg.Spawn,
		runners:  make(map[string]*runner),
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
	if e.spawn == nil {
		e.spawn = SpawnCLI
	}
	return e
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
		dir:      dir,
		agentCmd: agentCmd,
		prompt:   e.prompt,
		debounce: e.debounce,
		timeout:  e.timeout,
		spawn:    e.spawn,
	}
	e.runners[project] = r
	return r
}

// runner drives one project's auto-reply loop: it debounces triggering events
// and runs the agent with single-flight semantics — only one agent process runs
// at a time for this project, and events arriving mid-run schedule exactly one
// follow-up run.
type runner struct {
	dir      string
	agentCmd string
	prompt   string
	debounce time.Duration
	timeout  time.Duration
	spawn    SpawnFunc

	mu      sync.Mutex
	timer   *time.Timer
	running bool
	pending bool
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

// execute runs the agent with single-flight semantics. If a run is already in
// progress it sets pending and returns; the in-flight loop drains pending and
// runs once more, so concurrent triggers never start a second process.
func (r *runner) execute() {
	r.mu.Lock()
	if r.running {
		r.pending = true
		r.mu.Unlock()
		return
	}
	r.running = true
	r.mu.Unlock()

	for {
		r.runOnce()
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

// runOnce invokes the agent exactly once under a timeout. A panic in the spawn
// is recovered so a misbehaving agent driver can never crash the server; a
// non-zero exit or timeout is logged, not fatal.
func (r *runner) runOnce() {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("auto-reply: recovered from panic in agent spawn: %v", rec)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()
	log.Printf("auto-reply: invoking agent in %s", r.dir)
	if err := r.spawn(ctx, r.dir, r.agentCmd, r.prompt); err != nil {
		log.Printf("auto-reply: agent run failed: %v", err)
		return
	}
	log.Printf("auto-reply: agent run complete")
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

// SpawnCLI is the default SpawnFunc: it shells out to the agent CLI in dir,
// captures combined stdout/stderr to the server log, and returns the exit error.
func SpawnCLI(ctx context.Context, dir, agentCmd, prompt string) error {
	args := buildArgs(agentCmd, prompt)
	if len(args) == 0 {
		return &emptyCmdError{}
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		log.Printf("auto-reply: %s output:\n%s", args[0], strings.TrimRight(string(out), "\n"))
	}
	return err
}

type emptyCmdError struct{}

func (*emptyCmdError) Error() string { return "auto-reply: empty agent command" }
