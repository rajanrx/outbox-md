package config

import (
	"log"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Agent    AgentConfig    `json:"agent"    yaml:"agent"`
	Approval ApprovalConfig `json:"approval" yaml:"approval"`
	Webhook  WebhookConfig  `json:"webhook"  yaml:"webhook"`
	// Sources is an optional whitelist of folders and/or globs (relative to
	// OUTBOX_DIR) to ingest. Empty means "serve everything under OUTBOX_DIR"
	// (the default, backward-compatible behaviour).
	Sources []string `json:"sources" yaml:"sources"`
	// AutoUpdate controls whether a standalone `outbox up` self-updates to the
	// latest release on startup (throttled, best-effort). It defaults to true —
	// an absent key stays true, so only an explicit `auto_update: false` (or
	// OUTBOX_AUTO_UPDATE=false) opts out. Homebrew/Docker installs never
	// self-replace regardless of this flag (they update via their own channel).
	AutoUpdate bool `json:"autoUpdate" yaml:"auto_update"`
	// AutoReply enables the in-process auto-reply engine: when on, a human comment
	// spawns the agent CLI in-process (no separate runner). It is OPT-IN — it
	// defaults to false, so only an explicit `auto_reply: true` (or
	// OUTBOX_AUTO_REPLY=true, or the `-auto-reply` flag) turns it on.
	AutoReply bool `json:"autoReply" yaml:"auto_reply"`
	// AgentCmd is the command template the auto-reply engine spawns; the literal
	// token {prompt} is replaced by the instruction prompt as a single argv element
	// (no shell). Defaults to the Claude Code headless invocation.
	AgentCmd string `json:"agentCmd" yaml:"agent_cmd"`
}

type AgentConfig struct {
	BatchSize int `json:"batchSize" yaml:"batch_size"`
}

type ApprovalConfig struct {
	PostApprovalComments bool `json:"postApprovalComments" yaml:"post_approval_comments"`
}

// WebhookConfig points outbox at an external runner that should be notified of
// governance events. An empty URL disables webhooks (a no-op notifier is used).
// An empty Events slice means "all events enabled".
type WebhookConfig struct {
	URL    string   `json:"url"    yaml:"url"`
	Secret string   `json:"secret" yaml:"secret"`
	Events []string `json:"events" yaml:"events"`
}

// Defaults is the built-in configuration used when no outbox.yaml is present,
// and the floor every loaded config layers over.
func Defaults() Config {
	return Config{
		Agent: AgentConfig{BatchSize: 5},
		// AutoUpdate defaults to true so an outbox.yaml that omits the key (or has
		// no yaml at all) keeps self-update on; only an explicit false disables it.
		AutoUpdate: true,
		// AutoReply defaults to false — the in-process agent loop is strictly
		// opt-in. AgentCmd carries the default command template so it is populated
		// even when the engine is off (used the moment auto-reply is turned on).
		AutoReply: false,
		AgentCmd:  "claude -p {prompt} --allowedTools mcp__outbox-md__*",
		Approval:  ApprovalConfig{PostApprovalComments: true},
		// All four governance events are enabled by default. These string
		// literals mirror the event names emitted by internal/webhook; they are
		// duplicated here (rather than imported) to keep config free of a webhook
		// dependency.
		Webhook: WebhookConfig{Events: []string{
			"comment.created", "comment.replied", "comment.resolved", "document.approved",
		}},
	}
}

// Load reads outbox.yaml from the folder root, layered over Defaults(). A
// missing file yields the defaults; a malformed file logs and falls back to the
// defaults (startup never fails on config). batch_size below 1 is corrected.
func Load(dir string) Config {
	cfg := Defaults()
	// Layer outbox.yaml over the defaults when it exists; a missing or malformed
	// file just leaves the defaults in place (startup never fails on config).
	if data, err := os.ReadFile(filepath.Join(dir, "outbox.yaml")); err == nil {
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			log.Printf("outbox.yaml: invalid, using defaults: %v", err)
			cfg = Defaults()
		} else if cfg.Agent.BatchSize < 1 {
			cfg.Agent.BatchSize = Defaults().Agent.BatchSize
		}
	}
	// Environment overrides win over the file — and MUST apply even when there is
	// no outbox.yaml (env-only config is the common case for a containerized
	// server). Previously these sat behind the no-file early return and were
	// silently skipped, so OUTBOX_WEBHOOK_URL/SECRET were ignored without a yaml.
	if v := os.Getenv("OUTBOX_WEBHOOK_URL"); v != "" {
		cfg.Webhook.URL = v
	}
	if v := os.Getenv("OUTBOX_WEBHOOK_SECRET"); v != "" {
		cfg.Webhook.Secret = v
	}
	// OUTBOX_SOURCES is a comma-separated whitelist that overrides yaml sources —
	// the env-only counterpart of the file's `sources` list.
	if v := os.Getenv("OUTBOX_SOURCES"); v != "" {
		var out []string
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		cfg.Sources = out
	}
	// OUTBOX_AUTO_UPDATE overrides the file flag: false/0/no/off disables
	// self-update, true/1/yes/on enables it. Any other value leaves the current
	// (file or default-true) value untouched.
	if v := os.Getenv("OUTBOX_AUTO_UPDATE"); v != "" {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "false", "0", "no", "off":
			cfg.AutoUpdate = false
		case "true", "1", "yes", "on":
			cfg.AutoUpdate = true
		}
	}
	// OUTBOX_AUTO_REPLY overrides the file flag both ways (mirroring AutoUpdate):
	// true/1/yes/on turns the in-process agent loop on, false/0/no/off off. Any
	// other value leaves the current (file or default-false) value untouched.
	if v := os.Getenv("OUTBOX_AUTO_REPLY"); v != "" {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "false", "0", "no", "off":
			cfg.AutoReply = false
		case "true", "1", "yes", "on":
			cfg.AutoReply = true
		}
	}
	// OUTBOX_AGENT_CMD overrides the spawn command template.
	if v := os.Getenv("OUTBOX_AGENT_CMD"); v != "" {
		cfg.AgentCmd = v
	}
	// A yaml that explicitly sets `agent_cmd:` to empty would blank the template;
	// fall back to the default so the engine always has a runnable command.
	if strings.TrimSpace(cfg.AgentCmd) == "" {
		cfg.AgentCmd = Defaults().AgentCmd
	}
	return cfg
}

// Serves reports whether a doc path (relative to OUTBOX_DIR) is covered by the
// Sources whitelist, mirroring importMarkdown's semantics so the served set
// matches the imported set: a plain entry is a folder served recursively (exact
// match or prefix) or an exact file; an entry with glob metacharacters is
// matched single-level via filepath.Match. An empty whitelist serves everything.
// Every read surface (HTTP API and MCP) gates on this, so narrowing Sources
// hides docs consistently everywhere, not just in the browser.
func (c Config) Serves(docPath string) bool {
	if len(c.Sources) == 0 {
		return true
	}
	docPath = filepath.ToSlash(docPath)
	for _, src := range c.Sources {
		src = strings.TrimSuffix(filepath.ToSlash(strings.TrimSpace(src)), "/")
		if src == "" {
			continue
		}
		if strings.ContainsAny(src, "*?[") {
			if ok, _ := filepath.Match(src, docPath); ok {
				return true
			}
		} else if docPath == src || strings.HasPrefix(docPath, src+"/") {
			return true
		}
	}
	return false
}

// ProjectSources maps a served project's name to its loaded Config, so the
// runtime read guards enforce each project's OWN Sources whitelist against its
// own documents rather than a single global whitelist. The empty-string key is
// the single-folder mode (its Config carries the real single-dir Sources). A
// project absent from the map is treated as NOT served — orphaned documents left
// behind by a removed project stay hidden on every surface.
type ProjectSources map[string]Config

// Serves reports whether docPath in the named project is inside that project's
// active whitelist. An unknown project is not served (deny), which hides
// orphaned docs. A known project with an empty whitelist serves everything,
// exactly like Config.Serves.
func (m ProjectSources) Serves(project, docPath string) bool {
	cfg, ok := m[project]
	if !ok {
		return false
	}
	return cfg.Serves(docPath)
}

// Restricted reports whether the sources guards must run at all. It is false
// only in the classic single-folder mode with no whitelist (exactly one entry,
// keyed "", with empty Sources) — the original zero-extra-lookup fast path,
// preserved bit-for-bit. Any configured whitelist, or multi-project mode (where
// orphaned docs must be hidden), makes it restricted.
func (m ProjectSources) Restricted() bool {
	if len(m) != 1 {
		return true
	}
	cfg, ok := m[""]
	if !ok {
		return true
	}
	return len(cfg.Sources) > 0
}
