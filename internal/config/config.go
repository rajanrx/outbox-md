package config

import (
	"log"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// DefaultAgentRetries is the number of RETRY attempts after a failed agent run
// (total attempts = retries + 1). 5 gives the AI/CLI several chances to recover
// from a transient outage (the `signal: killed` case) before the run is
// abandoned. 0 disables retries (a single attempt).
const DefaultAgentRetries = 5

// DefaultAgentTimeout caps a single agent run. It is bumped from the historical
// 5m — a legitimate council/complex run was being killed at 5m — to 15m.
const DefaultAgentTimeout = 15 * time.Minute

// DefaultAgentConcurrency is how many agent runs the auto-reply engine may run
// AT ONCE per project (fan-out). 4 lets a burst of comments be worked in
// parallel instead of one-at-a-time; claim atomicity (store CAS) guarantees two
// agents never process the same comment. 1 reproduces the historical
// single-flight behaviour.
const DefaultAgentConcurrency = 4

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
	// Retries is the number of times a failed auto-reply run is retried (with
	// backoff) before it is abandoned; total attempts = Retries + 1. Absent ⇒
	// DefaultAgentRetries (5). An explicit 0 disables retries. Resolve with
	// ResolveRetries (which clamps a negative value to 0).
	Retries int `json:"retries" yaml:"retries"`
	// Timeout caps a single auto-reply run. It is a Go duration string ("15m",
	// "900s", …); a bare integer is read as minutes. Empty/invalid ⇒
	// DefaultAgentTimeout (15m). Resolve with ResolveTimeout.
	Timeout string `json:"timeout" yaml:"timeout"`
	// Concurrency is how many agent runs may execute AT ONCE per project. Absent ⇒
	// DefaultAgentConcurrency (4). 1 = single-flight (today's behaviour). Values
	// below 1 are clamped to 1. Resolve with ResolveConcurrency.
	Concurrency int `json:"concurrency" yaml:"concurrency"`
}

// ResolveRetries returns the effective retry count: an unset (zero-value from
// Defaults, which seeds 5) value keeps the default; a negative value is clamped
// to 0 (no retry). An explicit 0 is honoured as "no retry".
func (a AgentConfig) ResolveRetries() int {
	if a.Retries < 0 {
		return 0
	}
	return a.Retries
}

// ResolveConcurrency returns the effective per-project fan-out: an unset value
// (zero) keeps the Defaults()-seeded 4; any value below 1 is clamped to 1
// (single-flight). It is the count of agent runs allowed in flight at once per
// project.
func (a AgentConfig) ResolveConcurrency() int {
	if a.Concurrency < 1 {
		return 1
	}
	return a.Concurrency
}

// ResolveTimeout parses the configured Timeout into a duration, falling back to
// DefaultAgentTimeout when it is empty, unparseable, or non-positive. A bare
// integer (no unit) is interpreted as minutes, so `timeout: 15` means 15m.
func (a AgentConfig) ResolveTimeout() time.Duration {
	s := strings.TrimSpace(a.Timeout)
	if s == "" {
		return DefaultAgentTimeout
	}
	if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return d
	}
	if n, err := strconv.Atoi(s); err == nil && n > 0 {
		return time.Duration(n) * time.Minute
	}
	return DefaultAgentTimeout
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
		// Retries/Timeout seed the auto-reply resilience defaults; a yaml that omits
		// them keeps these, an explicit value overrides (0 retries = no retry).
		Agent: AgentConfig{BatchSize: 5, Retries: DefaultAgentRetries, Timeout: DefaultAgentTimeout.String(), Concurrency: DefaultAgentConcurrency},
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

// SourcesMatch reports whether the root-relative key matches the sources
// whitelist. It is the SINGLE sources-matching predicate shared by import and
// serve, so the imported set and the served set can never drift: a plain entry
// is a folder served recursively (exact match or prefix) or an exact file; an
// entry with glob metacharacters is matched single-level via filepath.Match. An
// empty whitelist matches everything. The key is always root-relative (the same
// base as the sources patterns and the document keys), so the pattern is never
// joined onto a spec dir — import and serve evaluate it against identical input.
func SourcesMatch(sources []string, key string) bool {
	if len(sources) == 0 {
		return true
	}
	key = filepath.ToSlash(key)
	for _, src := range sources {
		src = strings.TrimSuffix(filepath.ToSlash(strings.TrimSpace(src)), "/")
		if src == "" {
			continue
		}
		if strings.ContainsAny(src, "*?[") {
			if ok, _ := filepath.Match(src, key); ok {
				return true
			}
		} else if key == src || strings.HasPrefix(key, src+"/") {
			return true
		}
	}
	return false
}

// underDocs reports whether the root-relative key falls under at least one docs
// subtree. A docs entry of "." (or "") is the whole root and covers everything;
// an empty docs list is likewise treated as the whole root (backward-compatible
// with a project that carries only a sources filter). Matching uses clean path
// semantics so "docs" never spuriously covers "docsX" and a trailing slash on a
// docs entry is harmless.
func underDocs(docs []string, key string) bool {
	if len(docs) == 0 {
		return true
	}
	key = path.Clean(filepath.ToSlash(key))
	for _, d := range docs {
		d = path.Clean(filepath.ToSlash(strings.TrimSpace(d)))
		if d == "." || d == "/" {
			return true
		}
		if key == d || strings.HasPrefix(key, d+"/") {
			return true
		}
	}
	return false
}

// Serves reports whether a doc path (relative to OUTBOX_DIR) is covered by the
// Sources whitelist. It is the single-folder / global-config view of the shared
// SourcesMatch predicate: an empty whitelist serves everything. Every read
// surface (HTTP API and MCP) ultimately gates on the same predicate, so
// narrowing Sources hides docs consistently everywhere, not just in the browser.
func (c Config) Serves(docPath string) bool {
	return SourcesMatch(c.Sources, docPath)
}

// Coverage is a project's runtime served set: the UNION of its docs subtrees,
// optionally narrowed by a sources filter — both root-relative, evaluated
// against a root-relative document key. It is the ONE predicate that import and
// serve share, so a document is served iff it would also be imported. Docs comes
// from the registry (the subtrees a project serves); Sources comes from the
// project's outbox.yaml. An empty Docs is treated as the whole root; an empty
// Sources applies no filter.
type Coverage struct {
	// Docs is the set of root-relative docs subtrees the project serves. "." (or
	// "") is the whole root. Empty is treated as the whole root.
	Docs []string
	// Sources is the optional root-relative whitelist that further narrows the
	// docs union. Empty means "no filter" (serve every doc under the docs union).
	Sources []string
}

// Covers reports whether the root-relative key is served by this project: the
// key must be under at least one docs subtree AND pass the sources filter. This
// is exactly the predicate importMarkdown applies, so the served set equals the
// imported set (no import/serve drift).
func (cv Coverage) Covers(key string) bool {
	// Normalize the key ONCE so both predicates see the same path: underDocs
	// cleans internally but SourcesMatch only slashes, so a "./"/"../"/"//" key
	// could diverge between the two checks. Both re-normalize idempotently.
	key = path.Clean(filepath.ToSlash(key))
	return underDocs(cv.Docs, key) && SourcesMatch(cv.Sources, key)
}

// ProjectSources maps a served project's name to its runtime Coverage, so the
// read guards enforce each project's OWN docs union and sources whitelist
// against its own documents rather than a single global whitelist. The
// empty-string key is single-folder mode (Docs=["."], sources = the real
// single-dir whitelist). A project absent from the map is treated as NOT served
// — orphaned documents left behind by a removed project stay hidden everywhere.
type ProjectSources map[string]Coverage

// Serves reports whether docPath in the named project is inside that project's
// coverage (docs union + sources filter). An unknown project is not served
// (deny), which hides orphaned docs.
func (m ProjectSources) Serves(project, docPath string) bool {
	cv, ok := m[project]
	if !ok {
		return false
	}
	return cv.Covers(docPath)
}

// Restricted reports whether the sources guards must run at all. It is false
// only in the classic single-folder mode that serves its whole root with no
// whitelist (exactly one entry, keyed "", whole-root docs, empty Sources) — the
// original zero-extra-lookup fast path, preserved bit-for-bit. Any configured
// whitelist, a narrowed docs union, or multi-project mode (where orphaned docs
// must be hidden) makes it restricted.
func (m ProjectSources) Restricted() bool {
	if len(m) != 1 {
		return true
	}
	cv, ok := m[""]
	if !ok {
		return true
	}
	if len(cv.Sources) > 0 {
		return true
	}
	// Whole-root docs (empty, or every entry "."/"/") is unrestricted; a narrowed
	// docs union is a guard.
	for _, d := range cv.Docs {
		d = path.Clean(filepath.ToSlash(strings.TrimSpace(d)))
		if d != "." && d != "/" {
			return true
		}
	}
	return false
}
