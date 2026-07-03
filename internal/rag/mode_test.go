package rag

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// capture collects logged mode lines so transitions can be asserted.
type capture struct {
	mu    sync.Mutex
	lines []string
}

func (c *capture) logf(format string, args ...any) {
	c.mu.Lock()
	c.lines = append(c.lines, fmt.Sprintf(format, args...))
	c.mu.Unlock()
}

func TestModeStartupNoEmbedderIsKeywordOnly(t *testing.T) {
	c := &capture{}
	tr := NewModeTracker(false, c.logf)
	if tr.Mode() != ModeKeywordOnly {
		t.Fatalf("mode = %q, want keyword-only", tr.Mode())
	}
	if len(c.lines) != 1 || !strings.Contains(c.lines[0], "keyword-only") {
		t.Fatalf("startup log = %v, want a keyword-only line", c.lines)
	}
}

func TestModeStartupWithEmbedderIsSemantic(t *testing.T) {
	c := &capture{}
	tr := NewModeTracker(true, c.logf)
	if tr.Mode() != ModeSemantic {
		t.Fatalf("mode = %q, want semantic", tr.Mode())
	}
	if len(c.lines) != 1 || !strings.Contains(c.lines[0], "semantic") {
		t.Fatalf("startup log = %v, want a semantic line", c.lines)
	}
}

func TestModeDegradeAndRecoverTransitionsLogged(t *testing.T) {
	c := &capture{}
	tr := NewModeTracker(true, c.logf) // startup: semantic (1 line)

	tr.MarkDegraded()
	if tr.Mode() != ModeDegraded {
		t.Fatalf("after failure mode = %q, want degraded", tr.Mode())
	}
	tr.MarkHealthy()
	if tr.Mode() != ModeSemantic {
		t.Fatalf("after recovery mode = %q, want semantic", tr.Mode())
	}

	// startup + degrade + recover = 3 log lines (transitions are logged).
	if len(c.lines) != 3 {
		t.Fatalf("log lines = %d (%v), want 3", len(c.lines), c.lines)
	}
	if !strings.Contains(c.lines[1], "degraded") {
		t.Fatalf("second line = %q, want degraded transition", c.lines[1])
	}
}

func TestModeNoLogSpamWhenUnchanged(t *testing.T) {
	c := &capture{}
	tr := NewModeTracker(true, c.logf) // 1 startup line
	tr.MarkHealthy()                   // already semantic → no change, no log
	tr.MarkHealthy()
	if len(c.lines) != 1 {
		t.Fatalf("log lines = %d (%v), want 1 (no spam)", len(c.lines), c.lines)
	}
}

func TestModeKeywordOnlyIgnoresDegrade(t *testing.T) {
	c := &capture{}
	tr := NewModeTracker(false, c.logf) // keyword-only, no embedder
	tr.MarkDegraded()                   // no embedder to degrade → stays keyword-only
	if tr.Mode() != ModeKeywordOnly {
		t.Fatalf("mode = %q, want keyword-only (degrade is a no-op without embedder)", tr.Mode())
	}
	if len(c.lines) != 1 {
		t.Fatalf("log lines = %d, want 1", len(c.lines))
	}
}
