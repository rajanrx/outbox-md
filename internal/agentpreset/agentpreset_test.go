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

func TestResolveModelInjectsFlag(t *testing.T) {
	cases := map[string]string{
		"claude":  "claude --model opus -p {prompt} --allowedTools mcp__outbox-md__*",
		"codex":   "codex exec --dangerously-bypass-approvals-and-sandbox -m opus {prompt}",
		"copilot": "copilot --model opus -p {prompt}",
	}
	for name, want := range cases {
		got, ok := ResolveModel(name, "opus")
		if !ok {
			t.Fatalf("ResolveModel(%q, opus) should resolve", name)
		}
		if got != want {
			t.Fatalf("ResolveModel(%q, opus) = %q, want %q", name, got, want)
		}
	}
}

func TestResolveModelEmptyModelOmitsFlag(t *testing.T) {
	// An empty (or whitespace) model delegates to Resolve — the plain preset, no flag.
	for _, model := range []string{"", "   "} {
		got, ok := ResolveModel("claude", model)
		if !ok {
			t.Fatalf("ResolveModel(claude, %q) should resolve", model)
		}
		if want := "claude -p {prompt} --allowedTools mcp__outbox-md__*"; got != want {
			t.Fatalf("ResolveModel(claude, %q) = %q, want the flagless preset %q", model, got, want)
		}
	}
}

func TestResolveModelUnknownPreset(t *testing.T) {
	if _, ok := ResolveModel("nope", "opus"); ok {
		t.Fatal("ResolveModel on an unknown preset should not resolve")
	}
	// An unknown preset with an EMPTY model also does not resolve (delegates to Resolve).
	if _, ok := ResolveModel("nope", ""); ok {
		t.Fatal("ResolveModel(nope, \"\") should not resolve")
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
