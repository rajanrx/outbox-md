package agentpreset

import (
	"strings"
	"testing"
)

func TestValidModelRejectsInjection(t *testing.T) {
	good := []string{"", "gpt-5.5", "o3", "claude-opus-4-8", "voyage-3.5-lite", "org/model:tag"}
	for _, m := range good {
		if !ValidModel(m) {
			t.Errorf("ValidModel(%q) = false, want true", m)
		}
	}
	// Anything with whitespace or shell metacharacters would tokenize into extra
	// argv when spliced into a command template — must be rejected.
	bad := []string{"o3 --cd /tmp", "a b", "m;rm -rf /", "-rf", "$(x)", "m\n--x", "a|b", "a&b"}
	for _, m := range bad {
		if ValidModel(m) {
			t.Errorf("ValidModel(%q) = true, want false (injection vector)", m)
		}
	}
}

func TestResolveModelRefusesUnsafeModel(t *testing.T) {
	if _, ok := ResolveModel("claude", "o3 --cd /tmp"); ok {
		t.Fatal("ResolveModel spliced an unsafe model; want ok=false (no splice)")
	}
	got, ok := ResolveModel("claude", "opus")
	if !ok || !strings.Contains(got, "--model opus") {
		t.Fatalf("ResolveModel(claude, opus) = %q ok=%v, want the --model flag", got, ok)
	}
}
