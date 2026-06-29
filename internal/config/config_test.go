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
