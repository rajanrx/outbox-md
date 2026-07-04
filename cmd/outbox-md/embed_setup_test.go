package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rajanrx/outbox-md/internal/config"
	"github.com/rajanrx/outbox-md/internal/settings"
)

// TestEmbedCmdStringBuild verifies the embed_cmd value setup writes: default URL is
// omitted, a non-default URL is appended, and the model is always present.
func TestEmbedCmdStringBuild(t *testing.T) {
	if got := embedCmdString("/usr/local/bin/outbox", "embeddinggemma", defaultOllamaURL); got != "/usr/local/bin/outbox embed run --model embeddinggemma" {
		t.Fatalf("default-url command = %q", got)
	}
	if got := embedCmdString("outbox", "nomic-embed-text", "http://box:9999"); got != "outbox embed run --model nomic-embed-text --url http://box:9999" {
		t.Fatalf("custom-url command = %q", got)
	}
}

// TestEmbedSetupOllamaAbsent asserts that with ollama NOT on PATH, setup prints the
// install hint and does NOT write embed_cmd (no accidental config change).
func TestEmbedSetupOllamaAbsent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("OUTBOX_DIR", dir)
	// Force lookPath to report ollama as absent.
	orig := lookPath
	lookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	defer func() { lookPath = orig }()

	// Seed an outbox.yaml so we can prove setup does not touch it.
	cfgPath := filepath.Join(dir, "outbox.yaml")
	if err := os.WriteFile(cfgPath, []byte("auto_reply: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := embedSetup(nil, &out); err != nil {
		t.Fatalf("embed setup should not error when ollama is absent: %v", err)
	}
	if !strings.Contains(out.String(), "brew install ollama") {
		t.Fatalf("output should contain an install hint:\n%s", out.String())
	}
	data, _ := os.ReadFile(cfgPath)
	if strings.Contains(string(data), "embed_cmd") {
		t.Fatalf("embed_cmd must NOT be written when ollama is absent:\n%s", string(data))
	}
}

// TestEmbedSetupWritesEmbedCmd exercises the config-write path directly: writing
// embed_cmd via the settings path yields the expected `… embed run --model …`
// string, without needing ollama or an actual model pull. It also covers the
// create-if-missing outbox.yaml behaviour.
func TestEmbedSetupWritesEmbedCmd(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "outbox.yaml")
	if err := ensureConfigFile(cfgPath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cfgPath); err != nil {
		t.Fatalf("ensureConfigFile should have created %s: %v", cfgPath, err)
	}
	command := embedCmdString("/opt/outbox", "embeddinggemma", defaultOllamaURL)
	if err := settings.WriteKey(cfgPath, "embed_cmd", command, settings.KindString); err != nil {
		t.Fatal(err)
	}
	loaded := strings.TrimSpace(config.Load(dir).EmbedCmd)
	if loaded != "/opt/outbox embed run --model embeddinggemma" {
		t.Fatalf("embed_cmd = %q, want the embed-run command", loaded)
	}
}

// TestEmbedDispatchHelpAndUnknown verifies routing: bare `embed` prints help
// (no error), and an unknown subcommand errors helpfully.
func TestEmbedDispatchHelpAndUnknown(t *testing.T) {
	var help bytes.Buffer
	if err := run([]string{"embed"}, &help); err != nil {
		t.Fatalf("bare embed should not error: %v", err)
	}
	if !strings.Contains(help.String(), "outbox embed") || !strings.Contains(help.String(), "Examples:") {
		t.Fatalf("bare embed should print the embed help:\n%s", help.String())
	}

	var bogus bytes.Buffer
	err := run([]string{"embed", "bogus"}, &bogus)
	if err == nil {
		t.Fatal("unknown embed subcommand should error")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("error %q should name the bad subcommand", err.Error())
	}
}
