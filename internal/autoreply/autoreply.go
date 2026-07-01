// Package autoreply folds the reference runner (examples/runner) into the server
// as an in-process webhook.Notifier. When enabled, a human comment triggers a
// debounced, single-flight spawn of the agent CLI in the served directory — no
// separate runner process, no webhook receiver, no shared secret.
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
// shelling out to a real `claude`. dir is the served directory (cwd); agentCmd
// is the command template with a literal {prompt} token; prompt is the
// instruction substituted for that token.
type SpawnFunc func(ctx context.Context, dir, agentCmd, prompt string) error

// Config configures an Engine.
type Config struct {
	// Enabled gates the whole engine. When false, Enabled() is false and Fire is
	// a no-op — nothing is ever spawned.
	Enabled bool
	// Dir is the served directory used as the spawned agent's working directory
	// (where the outbox-md MCP is registered).
	Dir string
	// AgentCmd is the command template; the literal token {prompt} is replaced by
	// Prompt as a single argv element (no shell).
	AgentCmd string
	// Prompt is the instruction handed to the agent each run. Empty ⇒ DefaultPrompt.
	Prompt string
	// Debounce coalesces a burst into one run. Zero ⇒ DefaultDebounce.
	Debounce time.Duration
	// Timeout caps a single run. Zero ⇒ DefaultTimeout.
	Timeout time.Duration
	// Spawn runs the agent. Nil ⇒ the default exec-based spawn (SpawnCLI).
	Spawn SpawnFunc
}

// Engine implements webhook.Notifier. It debounces triggering events and runs
// the agent with single-flight semantics: only one agent process runs at a time,
// and events arriving mid-run schedule exactly one follow-up run.
type Engine struct {
	enabled  bool
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

// Ensure Engine satisfies the Notifier seam at compile time.
var _ webhook.Notifier = (*Engine)(nil)

// New builds an Engine from cfg, applying defaults for the zero-value fields.
func New(cfg Config) *Engine {
	e := &Engine{
		enabled:  cfg.Enabled,
		dir:      cfg.Dir,
		agentCmd: cfg.AgentCmd,
		prompt:   cfg.Prompt,
		debounce: cfg.Debounce,
		timeout:  cfg.Timeout,
		spawn:    cfg.Spawn,
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
// event fan-out): a triggering human event just (re)arms the debounce timer, and
// the actual agent run happens later on a background goroutine.
func (e *Engine) Fire(event string, _ any) {
	if !e.enabled || !triggers(event) {
		return
	}
	e.trigger()
}

// trigger (re)arms the debounce timer. Repeated calls within the window coalesce
// into a single execute.
func (e *Engine) trigger() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.timer != nil {
		e.timer.Stop()
	}
	e.timer = time.AfterFunc(e.debounce, func() { go e.execute() })
}

// execute runs the agent with single-flight semantics. If a run is already in
// progress it sets pending and returns; the in-flight loop drains pending and
// runs once more, so concurrent triggers never start a second process.
func (e *Engine) execute() {
	e.mu.Lock()
	if e.running {
		e.pending = true
		e.mu.Unlock()
		return
	}
	e.running = true
	e.mu.Unlock()

	for {
		e.runOnce()
		e.mu.Lock()
		if e.pending {
			e.pending = false
			e.mu.Unlock()
			continue
		}
		e.running = false
		e.mu.Unlock()
		return
	}
}

// runOnce invokes the agent exactly once under a timeout. A panic in the spawn
// is recovered so a misbehaving agent driver can never crash the server; a
// non-zero exit or timeout is logged, not fatal.
func (e *Engine) runOnce() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("auto-reply: recovered from panic in agent spawn: %v", r)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), e.timeout)
	defer cancel()
	log.Printf("auto-reply: invoking agent")
	if err := e.spawn(ctx, e.dir, e.agentCmd, e.prompt); err != nil {
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
