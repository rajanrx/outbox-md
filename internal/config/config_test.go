package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func write(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "outbox.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadDefaultsWhenAbsent(t *testing.T) {
	cfg := Load(t.TempDir())
	if cfg.Agent.BatchSize != 5 || !cfg.Approval.PostApprovalComments {
		t.Fatalf("defaults = %+v, want {batch 5, postApproval true}", cfg)
	}
}

func TestConcurrencyDefaultsTo4WhenAbsent(t *testing.T) {
	cfg := Load(t.TempDir())
	if got := cfg.Agent.ResolveConcurrency(); got != DefaultAgentConcurrency {
		t.Fatalf("absent concurrency = %d, want %d (Defaults seed)", got, DefaultAgentConcurrency)
	}
}

func TestConcurrencyFromYAML(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "agent:\n  concurrency: 8\n")
	if got := Load(dir).Agent.ResolveConcurrency(); got != 8 {
		t.Fatalf("concurrency = %d, want 8", got)
	}
}

func TestConcurrencyBelowOneClampedToOne(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "agent:\n  concurrency: 0\n")
	if got := Load(dir).Agent.ResolveConcurrency(); got != 1 {
		t.Fatalf("explicit concurrency: 0 = %d, want 1 (single-flight)", got)
	}
	if got := (AgentConfig{Concurrency: -3}).ResolveConcurrency(); got != 1 {
		t.Fatalf("negative concurrency = %d, want 1", got)
	}
}

func TestLoadAutoUpdateDefaultsTrue(t *testing.T) {
	if !Load(t.TempDir()).AutoUpdate {
		t.Fatal("auto_update should default to true when the key is absent")
	}
}

func TestLoadAutoUpdateDisabledFromYAML(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "auto_update: false\n")
	if Load(dir).AutoUpdate {
		t.Fatal("auto_update: false should disable self-update")
	}
}

func TestLoadAutoUpdateEnvOverride(t *testing.T) {
	t.Setenv("OUTBOX_AUTO_UPDATE", "false")
	if Load(t.TempDir()).AutoUpdate {
		t.Fatal("OUTBOX_AUTO_UPDATE=false should disable self-update")
	}
}

func TestLoadOverridesBatchSizeKeepsOtherDefaults(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "agent:\n  batch_size: 3\n")
	cfg := Load(dir)
	if cfg.Agent.BatchSize != 3 {
		t.Errorf("batch_size = %d, want 3", cfg.Agent.BatchSize)
	}
	if !cfg.Approval.PostApprovalComments {
		t.Error("post_approval_comments should stay default true when omitted")
	}
}

func TestLoadDisablePostApproval(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "approval:\n  post_approval_comments: false\n")
	if Load(dir).Approval.PostApprovalComments {
		t.Error("post_approval_comments = true, want false")
	}
}

func TestLoadMalformedFallsBackToDefaults(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "agent: [this is not valid yaml")
	if Load(dir).Agent.BatchSize != 5 {
		t.Error("malformed file should fall back to default batch_size 5")
	}
}

func TestLoadZeroBatchSizeCorrected(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "agent:\n  batch_size: 0\n")
	if Load(dir).Agent.BatchSize != 5 {
		t.Error("batch_size 0 should be corrected to default 5")
	}
}

func TestDefaultsEnableAllWebhookEvents(t *testing.T) {
	w := Defaults().Webhook
	if w.URL != "" || w.Secret != "" {
		t.Errorf("default webhook url/secret = %q/%q, want empty", w.URL, w.Secret)
	}
	if len(w.Events) != 4 {
		t.Fatalf("default webhook events = %v, want all four", w.Events)
	}
}

func TestLoadWebhookFromYAML(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "webhook:\n  url: http://runner/hook\n  secret: shh\n  events: [comment.created]\n")
	w := Load(dir).Webhook
	if w.URL != "http://runner/hook" || w.Secret != "shh" {
		t.Errorf("webhook = %+v, want url/secret from yaml", w)
	}
	if len(w.Events) != 1 || w.Events[0] != "comment.created" {
		t.Errorf("events = %v, want [comment.created]", w.Events)
	}
}

func TestLoadSourcesFromYAML(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "sources:\n  - specs\n  - drafts/*.md\n")
	got := Load(dir).Sources
	want := []string{"specs", "drafts/*.md"}
	if len(got) != len(want) {
		t.Fatalf("sources = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sources = %v, want %v", got, want)
		}
	}
}

func TestLoadSourcesDefaultEmpty(t *testing.T) {
	if s := Load(t.TempDir()).Sources; len(s) != 0 {
		t.Fatalf("sources = %v, want empty (serve everything)", s)
	}
}

func TestLoadSourcesEnvOverride(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "sources:\n  - specs\n")
	t.Setenv("OUTBOX_SOURCES", "a, b/*.md ,")
	got := Load(dir).Sources
	if len(got) != 2 || got[0] != "a" || got[1] != "b/*.md" {
		t.Fatalf("sources = %v, want env override [a b/*.md] (trimmed, empties dropped)", got)
	}
}

func TestLoadWebhookEnvOverrides(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "webhook:\n  url: http://file/hook\n")
	t.Setenv("OUTBOX_WEBHOOK_URL", "http://env/hook")
	t.Setenv("OUTBOX_WEBHOOK_SECRET", "env-secret")
	w := Load(dir).Webhook
	if w.URL != "http://env/hook" {
		t.Errorf("url = %q, want env override", w.URL)
	}
	if w.Secret != "env-secret" {
		t.Errorf("secret = %q, want env override", w.Secret)
	}
}

// Regression: env overrides must apply even when there is NO outbox.yaml — the
// normal case for a containerized server configured purely via env. Previously
// Load returned early on a missing file, silently dropping OUTBOX_WEBHOOK_URL /
// OUTBOX_WEBHOOK_SECRET, so webhooks never fired without a yaml present.
func TestLoadWebhookEnvOverridesWithoutYAML(t *testing.T) {
	dir := t.TempDir() // deliberately no outbox.yaml
	t.Setenv("OUTBOX_WEBHOOK_URL", "http://env/hook")
	t.Setenv("OUTBOX_WEBHOOK_SECRET", "env-secret")
	w := Load(dir).Webhook
	if w.URL != "http://env/hook" {
		t.Errorf("url = %q, want env override applied without a yaml file", w.URL)
	}
	if w.Secret != "env-secret" {
		t.Errorf("secret = %q, want env override applied without a yaml file", w.Secret)
	}
}

// --- auto-reply config ---

func TestLoadAutoReplyDefaultsFalse(t *testing.T) {
	if Load(t.TempDir()).AutoReply {
		t.Fatal("auto_reply should default to false (opt-in)")
	}
}

func TestLoadAutoReplyEnabledFromYAML(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "auto_reply: true\n")
	if !Load(dir).AutoReply {
		t.Fatal("auto_reply: true should enable auto-reply")
	}
}

func TestLoadAutoReplyEnvOverride(t *testing.T) {
	// env true with no yaml.
	t.Setenv("OUTBOX_AUTO_REPLY", "true")
	if !Load(t.TempDir()).AutoReply {
		t.Fatal("OUTBOX_AUTO_REPLY=true should enable auto-reply")
	}
}

func TestLoadAutoReplyEnvOverridesYAMLFalse(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "auto_reply: true\n")
	t.Setenv("OUTBOX_AUTO_REPLY", "false")
	if Load(dir).AutoReply {
		t.Fatal("OUTBOX_AUTO_REPLY=false should override yaml auto_reply: true")
	}
}

func TestLoadAgentCmdDefault(t *testing.T) {
	got := Load(t.TempDir()).AgentCmd
	want := "claude -p {prompt} --allowedTools mcp__outbox-md__*"
	if got != want {
		t.Fatalf("agent_cmd default = %q, want %q", got, want)
	}
}

func TestLoadAgentCmdFromYAML(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "agent_cmd: my-agent {prompt}\n")
	if got := Load(dir).AgentCmd; got != "my-agent {prompt}" {
		t.Fatalf("agent_cmd = %q, want yaml override", got)
	}
}

func TestLoadAgentCmdEnvOverride(t *testing.T) {
	t.Setenv("OUTBOX_AGENT_CMD", "echo ran {prompt}")
	if got := Load(t.TempDir()).AgentCmd; got != "echo ran {prompt}" {
		t.Fatalf("agent_cmd = %q, want env override", got)
	}
}

func TestLoadAgentCmdEmptyFallsBackToDefault(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "agent_cmd: \"\"\n")
	want := "claude -p {prompt} --allowedTools mcp__outbox-md__*"
	if got := Load(dir).AgentCmd; got != want {
		t.Fatalf("empty agent_cmd = %q, want fallback to default", got)
	}
}

// --- Coverage: the unified docs-union + root-relative-sources predicate ---

// P1 core: docs gate even with an empty sources filter — a key outside the docs
// list is not covered, a key inside is.
func TestCoverageGatesOnDocsUnionWithEmptySources(t *testing.T) {
	cv := Coverage{Docs: []string{"docs/specs"}}
	if !cv.Covers("docs/specs/a.md") {
		t.Fatal("docs/specs/a.md must be covered (under the docs union)")
	}
	if cv.Covers("other/x.md") {
		t.Fatal("other/x.md must NOT be covered (outside the docs union)")
	}
}

// sources-as-subpath: whole-root docs narrowed by a sources filter serves only
// the sources subtree — the classic "serve part of a repo" case.
func TestCoverageSourcesAsSubpath(t *testing.T) {
	cv := Coverage{Docs: []string{"."}, Sources: []string{"drafts"}}
	if !cv.Covers("drafts/x.md") {
		t.Fatal("drafts/x.md must be covered")
	}
	if cv.Covers("specs/y.md") {
		t.Fatal("specs/y.md must NOT be covered (outside the sources filter)")
	}
}

// multi-docs union: several docs subtrees, no sources → every subtree served and
// nothing else.
func TestCoverageMultiDocsUnion(t *testing.T) {
	cv := Coverage{Docs: []string{"specs", "api-specs"}}
	for _, k := range []string{"specs/a.md", "api-specs/b.md"} {
		if !cv.Covers(k) {
			t.Fatalf("%s must be covered (in the docs union)", k)
		}
	}
	if cv.Covers("other/c.md") {
		t.Fatal("other/c.md must NOT be covered (outside every docs subtree)")
	}
}

// clean-path semantics: a docs entry "docs" must not spuriously cover a sibling
// "docsX" (no lexical off-by-one), and a trailing slash is harmless.
func TestCoverageCleanPathBoundaries(t *testing.T) {
	cv := Coverage{Docs: []string{"docs/"}}
	if !cv.Covers("docs/a.md") || !cv.Covers("docs") {
		t.Fatal("docs and docs/a.md must be covered")
	}
	if cv.Covers("docsX/a.md") {
		t.Fatal("docsX/a.md must NOT be covered by a docs entry (off-by-one)")
	}
}

// The docs union AND the sources filter both apply: a key must be under docs AND
// pass sources.
func TestCoverageDocsAndSourcesBothApply(t *testing.T) {
	cv := Coverage{Docs: []string{"docs/specs"}, Sources: []string{"docs/specs/keep"}}
	if !cv.Covers("docs/specs/keep/a.md") {
		t.Fatal("docs/specs/keep/a.md must be covered")
	}
	if cv.Covers("docs/specs/drop/b.md") {
		t.Fatal("docs/specs/drop/b.md is under docs but outside sources → not covered")
	}
	if cv.Covers("other/x.md") {
		t.Fatal("other/x.md is outside docs → not covered")
	}
}

// Restricted: single-folder whole-root with no filter is the unrestricted fast
// path; a narrowed docs union or any sources filter makes it restricted, and
// multi-project is always restricted.
func TestProjectSourcesRestricted(t *testing.T) {
	if (ProjectSources{"": Coverage{}}).Restricted() {
		t.Fatal("single \"\" whole-root, no sources → unrestricted")
	}
	if (ProjectSources{"": Coverage{Docs: []string{"."}}}).Restricted() {
		t.Fatal("single \"\" docs=[.] no sources → unrestricted")
	}
	if !(ProjectSources{"": Coverage{Sources: []string{"drafts"}}}).Restricted() {
		t.Fatal("a sources filter → restricted")
	}
	if !(ProjectSources{"": Coverage{Docs: []string{"docs/specs"}}}).Restricted() {
		t.Fatal("a narrowed docs union → restricted")
	}
	if !(ProjectSources{"a": Coverage{}, "b": Coverage{}}).Restricted() {
		t.Fatal("multi-project → always restricted")
	}
}

func TestDefaultsSeedAgentRetriesAndTimeout(t *testing.T) {
	cfg := Load(t.TempDir())
	if got := cfg.Agent.ResolveRetries(); got != DefaultAgentRetries {
		t.Fatalf("default retries = %d, want %d", got, DefaultAgentRetries)
	}
	if got := cfg.Agent.ResolveTimeout(); got != DefaultAgentTimeout {
		t.Fatalf("default timeout = %v, want %v (15m)", got, DefaultAgentTimeout)
	}
}

func TestAgentRetriesFromYAML(t *testing.T) {
	dir := t.TempDir()
	// An explicit 0 must be honoured as "no retry", not reset to the default.
	write(t, dir, "agent:\n  retries: 0\n")
	if got := Load(dir).Agent.ResolveRetries(); got != 0 {
		t.Fatalf("retries: 0 resolved to %d, want 0 (no retry)", got)
	}
	write(t, dir, "agent:\n  retries: 8\n")
	if got := Load(dir).Agent.ResolveRetries(); got != 8 {
		t.Fatalf("retries: 8 resolved to %d, want 8", got)
	}
	// Negative clamps to 0.
	if got := (AgentConfig{Retries: -3}).ResolveRetries(); got != 0 {
		t.Fatalf("negative retries resolved to %d, want 0", got)
	}
}

func TestAgentTimeoutParsing(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "agent:\n  timeout: 20m\n")
	if got := Load(dir).Agent.ResolveTimeout(); got != 20*time.Minute {
		t.Fatalf("timeout 20m = %v, want 20m", got)
	}
	// Bare integer → minutes.
	if got := (AgentConfig{Timeout: "3"}).ResolveTimeout(); got != 3*time.Minute {
		t.Fatalf("bare int timeout = %v, want 3m", got)
	}
	// Seconds duration.
	if got := (AgentConfig{Timeout: "90s"}).ResolveTimeout(); got != 90*time.Second {
		t.Fatalf("90s timeout = %v, want 90s", got)
	}
	// Empty/invalid → default.
	if got := (AgentConfig{Timeout: ""}).ResolveTimeout(); got != DefaultAgentTimeout {
		t.Fatalf("empty timeout = %v, want default", got)
	}
	if got := (AgentConfig{Timeout: "garbage"}).ResolveTimeout(); got != DefaultAgentTimeout {
		t.Fatalf("invalid timeout = %v, want default", got)
	}
}

// TestCoversNormalizesKeyOnce — a key with a "./" segment must be cleaned before
// BOTH predicates: underDocs cleans internally, but SourcesMatch only slashed, so
// an un-cleaned key could pass the docs-union yet miss the sources match. Covers
// now normalizes once so the two agree.
func TestCoversNormalizesKeyOnce(t *testing.T) {
	cv := Coverage{Docs: []string{"."}, Sources: []string{"drafts"}}
	if !cv.Covers("./drafts/a.md") {
		t.Fatal(`Covers("./drafts/a.md") = false; want true (key must be cleaned before SourcesMatch)`)
	}
	if cv.Covers("notes/a.md") {
		t.Fatal(`Covers("notes/a.md") = true; want false (outside the sources whitelist)`)
	}
}

// --- council guardrails (design §6) ---

func TestCouncilDefaultsWhenAbsent(t *testing.T) {
	cfg := Load(t.TempDir())
	if got := cfg.ResolveCouncilRounds(); got != DefaultCouncilRounds {
		t.Fatalf("council_rounds default = %d, want %d", got, DefaultCouncilRounds)
	}
	if got := cfg.ResolveCouncilBudget(); got != DefaultCouncilBudget {
		t.Fatalf("council_budget default = %d, want %d", got, DefaultCouncilBudget)
	}
	if got := cfg.ResolveCouncilDeadlockThreshold(); got != DefaultCouncilDeadlockThreshold {
		t.Fatalf("council_deadlock_threshold default = %d, want %d", got, DefaultCouncilDeadlockThreshold)
	}
}

func TestCouncilFromYAML(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "council_rounds: 3\ncouncil_budget: 50000\ncouncil_deadlock_threshold: 40\n")
	cfg := Load(dir)
	if cfg.ResolveCouncilRounds() != 3 {
		t.Fatalf("council_rounds = %d, want 3", cfg.ResolveCouncilRounds())
	}
	if cfg.ResolveCouncilBudget() != 50000 {
		t.Fatalf("council_budget = %d, want 50000", cfg.ResolveCouncilBudget())
	}
	if cfg.ResolveCouncilDeadlockThreshold() != 40 {
		t.Fatalf("council_deadlock_threshold = %d, want 40", cfg.ResolveCouncilDeadlockThreshold())
	}
}

func TestCouncilZeroOrNegativeFallsBackToDefault(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "council_rounds: 0\ncouncil_budget: 0\ncouncil_deadlock_threshold: 0\n")
	cfg := Load(dir)
	if cfg.ResolveCouncilRounds() != DefaultCouncilRounds {
		t.Fatalf("zero council_rounds resolved to %d, want default %d", cfg.ResolveCouncilRounds(), DefaultCouncilRounds)
	}
	if cfg.ResolveCouncilBudget() != DefaultCouncilBudget {
		t.Fatalf("zero council_budget resolved to %d, want default %d", cfg.ResolveCouncilBudget(), DefaultCouncilBudget)
	}
	if cfg.ResolveCouncilDeadlockThreshold() != DefaultCouncilDeadlockThreshold {
		t.Fatalf("zero council_deadlock_threshold resolved to %d, want default %d", cfg.ResolveCouncilDeadlockThreshold(), DefaultCouncilDeadlockThreshold)
	}
	// Negative on a raw Config likewise falls back.
	if (Config{CouncilRounds: -5}).ResolveCouncilRounds() != DefaultCouncilRounds {
		t.Fatal("negative council_rounds should fall back to default")
	}
}
