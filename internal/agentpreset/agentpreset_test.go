package agentpreset

import "testing"

func TestResolveKnownPresets(t *testing.T) {
	cases := map[string]string{
		"claude":  "claude -p {prompt} --allowedTools mcp__outbox-md__*",
		"codex":   "codex exec --dangerously-bypass-approvals-and-sandbox {prompt}",
		"copilot": "copilot -p {prompt}",
	}
	for name, want := range cases {
		got, ok := Resolve(name)
		if !ok {
			t.Fatalf("preset %q should resolve", name)
		}
		if got != want {
			t.Fatalf("preset %q = %q, want %q", name, got, want)
		}
	}
}

func TestResolveUnknownPreset(t *testing.T) {
	if _, ok := Resolve("nope"); ok {
		t.Fatal("unknown preset should not resolve")
	}
	if _, err := ResolveOrError("nope"); err == nil {
		t.Fatal("ResolveOrError should error on an unknown preset")
	}
	if _, err := ResolveOrError("claude"); err != nil {
		t.Fatalf("ResolveOrError(claude): %v", err)
	}
}

func TestNamesSorted(t *testing.T) {
	names := Names()
	want := []string{"claude", "codex", "copilot"}
	if len(names) != len(want) {
		t.Fatalf("Names = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("Names = %v, want %v (sorted)", names, want)
		}
	}
}
