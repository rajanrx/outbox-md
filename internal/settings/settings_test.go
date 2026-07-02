package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rajanrx/outbox-md/internal/config"
)

// TestWriteKeyRoundTrip covers the three shapes the write path must handle:
// updating an existing key, appending to a real mapping, and appending to a
// comment-only (nullish) file — all preserving comments/unmanaged keys, and a
// string value with YAML metacharacters must re-parse to exactly what was written.
func TestWriteKeyRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "outbox.yaml")
	seed := "# guidance comment\nsources:\n  - docs/specs\nauto_reply: false\n"
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	// Update an existing bool key.
	if err := WriteKey(path, "auto_reply", "true", KindBool); err != nil {
		t.Fatalf("write bool: %v", err)
	}
	// Append a new string key with metacharacters that require quoting.
	const cmd = "claude -p {prompt} --allowedTools *"
	if err := WriteKey(path, "agent_cmd", cmd, KindString); err != nil {
		t.Fatalf("write string: %v", err)
	}

	txt, _ := os.ReadFile(path)
	if !strings.Contains(string(txt), "guidance comment") || !strings.Contains(string(txt), "docs/specs") {
		t.Fatalf("comment / unmanaged key clobbered:\n%s", txt)
	}
	cfg := config.Load(dir)
	if !cfg.AutoReply {
		t.Fatalf("auto_reply not persisted: %+v", cfg)
	}
	if cfg.AgentCmd != cmd {
		t.Fatalf("agent_cmd round-trip = %q, want %q", cfg.AgentCmd, cmd)
	}
}

// TestWriteKeyCommentOnly verifies the nullish/comment-only file branch: a file
// that is all comments must gain the key while keeping every comment verbatim,
// and a string value must still be correctly quoted.
func TestWriteKeyCommentOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "outbox.yaml")
	if err := os.WriteFile(path, []byte("# only comments here\n# another line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteKey(path, "agent_cmd", "echo {prompt}", KindString); err != nil {
		t.Fatalf("write: %v", err)
	}
	txt, _ := os.ReadFile(path)
	if !strings.Contains(string(txt), "only comments here") || !strings.Contains(string(txt), "another line") {
		t.Fatalf("starter comments lost:\n%s", txt)
	}
	if cfg := config.Load(dir); cfg.AgentCmd != "echo {prompt}" {
		t.Fatalf("agent_cmd = %q on comment-only append", cfg.AgentCmd)
	}
}

// TestValidate covers type validation for each kind.
func TestValidate(t *testing.T) {
	if v, err := Validate(KindBool, "auto_reply", "yes"); err != nil || v != "true" {
		t.Fatalf("bool yes → %q,%v", v, err)
	}
	if _, err := Validate(KindBool, "auto_reply", "maybe"); err == nil {
		t.Fatal("bool maybe should error")
	}
	if v, err := Validate(KindInt, "n", " 7 "); err != nil || v != "7" {
		t.Fatalf("int → %q,%v", v, err)
	}
	if _, err := Validate(KindInt, "n", "x"); err == nil {
		t.Fatal("int x should error")
	}
	if v, err := Validate(KindString, "s", "  hi  "); err != nil || v != "hi" {
		t.Fatalf("string → %q,%v", v, err)
	}
}

// TestCouncilIntFieldsEditableAndWrite: the three council guardrails are editable
// int fields, and an int value written via WriteKey round-trips through config.
func TestCouncilIntFieldsEditableAndWrite(t *testing.T) {
	for _, key := range []string{"council_rounds", "council_budget", "council_deadlock_threshold"} {
		f, ok := FieldByKey(key)
		if !ok {
			t.Fatalf("%s should be an editable field", key)
		}
		if f.Kind != KindInt {
			t.Fatalf("%s kind = %q, want int", key, f.Kind)
		}
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "outbox.yaml")
	if err := os.WriteFile(path, []byte("# guidance\nauto_reply: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteKey(path, "council_rounds", "3", KindInt); err != nil {
		t.Fatalf("write int: %v", err)
	}
	txt, _ := os.ReadFile(path)
	if !strings.Contains(string(txt), "council_rounds: 3") {
		t.Fatalf("council_rounds not written as bare int:\n%s", txt)
	}
	if !strings.Contains(string(txt), "guidance") {
		t.Fatalf("comment clobbered:\n%s", txt)
	}
	if cfg := config.Load(dir); cfg.ResolveCouncilRounds() != 3 {
		t.Fatalf("council_rounds round-trip = %d, want 3", cfg.ResolveCouncilRounds())
	}
}
