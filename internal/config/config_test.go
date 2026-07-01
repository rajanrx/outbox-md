package config

import (
	"os"
	"path/filepath"
	"testing"
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
