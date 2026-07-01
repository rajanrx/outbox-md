package main

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the runner's full configuration, read from the environment. Every
// field has a default so the runner starts with zero setup; the env var names
// and defaults are identical across the Go, Node, and Python implementations.
type Config struct {
	// Addr is the listen address for the webhook HTTP server (RUNNER_ADDR).
	Addr string
	// Secret is the shared HMAC secret (OUTBOX_WEBHOOK_SECRET). Empty ⇒ requests
	// are accepted only when AllowUnsigned is set (default-deny otherwise).
	Secret string
	// AllowUnsigned permits unsigned requests when no Secret is set
	// (RUNNER_ALLOW_UNSIGNED=1|true). Default false ⇒ no secret means reject.
	AllowUnsigned bool
	// MaxBodyBytes caps the raw request body read before auth (RUNNER_MAX_BODY_BYTES);
	// a larger body is rejected with 413 before any work, bounding pre-auth memory.
	MaxBodyBytes int64
	// Events is the set of event names the runner acts on (RUNNER_EVENTS). Any
	// event not in this set is acknowledged with 200 and ignored.
	Events map[string]bool
	// Debounce coalesces a burst of events into a single agent run.
	Debounce time.Duration

	// AgentMode selects the backend: "cli" (default) or "api".
	AgentMode string
	// AgentCmd is the cli-mode command template; the literal token {prompt} is
	// replaced by the instruction prompt as a single argv element (no shell).
	AgentCmd string
	// Prompt is the instruction handed to the agent on each run.
	Prompt string

	// MCPURL is the outbox-md Streamable-HTTP MCP endpoint (api mode).
	MCPURL string
	// APIKey is the Anthropic API key (api mode). Empty ⇒ api mode errors.
	APIKey string
	// Model is the Anthropic model id (api mode), overridable so the reference
	// never pins a single id.
	Model string
	// AgentID is the identity recorded on claims/suggestions/replies.
	AgentID string
}

// DefaultPrompt is the instruction the runner hands the agent. It encodes the
// outbox loop and the human-only invariant so a fresh agent process knows the
// rules without reading AGENTS.md.
const DefaultPrompt = "Process the open outbox comments using the outbox-md tools — " +
	"read each comment's excerpt + thread, claim it, then call mark_processing so the human " +
	"sees it's being worked on (heartbeat it on long runs), then propose_suggestion (a " +
	"tracked-change edit) or reply_in_thread; honor the anti-sycophancy guidance (a comment " +
	"is not an order, disagree when the evidence warrants); never resolve, accept, or approve " +
	"(those are human-only)."

// DefaultAgentCmd targets Claude Code in headless mode and pre-authorizes the
// outbox-md MCP tools so the run is non-interactive.
const DefaultAgentCmd = "claude -p {prompt} --allowedTools mcp__outbox-md__*"

// DefaultMaxBodyBytes is the request-body cap when RUNNER_MAX_BODY_BYTES is unset
// or invalid: 1 MiB, plenty for these webhooks.
const DefaultMaxBodyBytes int64 = 1 << 20

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// envBool reports whether key is set to "1" or "true" (case-insensitive).
func envBool(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true":
		return true
	}
	return false
}

// parseEvents splits a comma-separated list into a set, trimming blanks.
func parseEvents(csv string) map[string]bool {
	set := map[string]bool{}
	for _, e := range strings.Split(csv, ",") {
		if e = strings.TrimSpace(e); e != "" {
			set[e] = true
		}
	}
	return set
}

// LoadConfig reads the full configuration from the environment, applying the
// shared defaults.
func LoadConfig() Config {
	debounceMS, err := strconv.Atoi(env("RUNNER_DEBOUNCE_MS", "1500"))
	if err != nil || debounceMS < 0 {
		debounceMS = 1500
	}
	maxBody, err := strconv.ParseInt(env("RUNNER_MAX_BODY_BYTES", "1048576"), 10, 64)
	if err != nil || maxBody <= 0 {
		maxBody = DefaultMaxBodyBytes
	}
	return Config{
		Addr:          env("RUNNER_ADDR", ":8787"),
		Secret:        os.Getenv("OUTBOX_WEBHOOK_SECRET"),
		AllowUnsigned: envBool("RUNNER_ALLOW_UNSIGNED"),
		MaxBodyBytes:  maxBody,
		Events:        parseEvents(env("RUNNER_EVENTS", "comment.created,comment.replied")),
		Debounce:      time.Duration(debounceMS) * time.Millisecond,
		AgentMode:     env("RUNNER_AGENT_MODE", "cli"),
		AgentCmd:      env("RUNNER_AGENT_CMD", DefaultAgentCmd),
		Prompt:        env("RUNNER_PROMPT", DefaultPrompt),
		MCPURL:        env("OUTBOX_MCP_URL", "http://localhost:8181/mcp"),
		APIKey:        os.Getenv("ANTHROPIC_API_KEY"),
		Model:         env("ANTHROPIC_MODEL", "claude-sonnet-4-5"),
		AgentID:       env("RUNNER_AGENT_ID", "outbox-runner"),
	}
}
