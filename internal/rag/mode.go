package rag

import (
	"log"
	"sync"
)

// RetrievalMode is the KB's current retrieval posture, exposed so the CLI/UI and
// logs read the same fact (design §5.5). A fallback to keyword search is always
// visible, never silent.
type RetrievalMode string

const (
	// ModeSemantic — an embedder is configured and healthy; vector + FTS5 hybrid.
	ModeSemantic RetrievalMode = "semantic"
	// ModeKeywordOnly — no embedder configured/detected; FTS5 (or LIKE) only.
	ModeKeywordOnly RetrievalMode = "keyword-only"
	// ModeDegraded — an embedder IS configured but currently failing; FTS5 serves
	// meanwhile and stale chunks retry.
	ModeDegraded RetrievalMode = "degraded"
)

// ModeTracker holds the current retrieval mode and logs the mode at startup and
// on every transition (design §5.5). It is safe for concurrent use — the watcher
// re-embed pass and search read/update it from different goroutines.
type ModeTracker struct {
	mu   sync.Mutex
	mode RetrievalMode
	logf func(format string, args ...any)
}

// NewModeTracker seeds the initial mode from whether an embedder is configured and
// logs it once at startup. hasEmbedder=false ⇒ keyword-only; true ⇒ semantic
// (optimistic until the first embed failure flips it to degraded). logf may be nil
// (defaults to the standard logger).
func NewModeTracker(hasEmbedder bool, logf func(format string, args ...any)) *ModeTracker {
	if logf == nil {
		logf = log.Printf
	}
	m := ModeKeywordOnly
	if hasEmbedder {
		m = ModeSemantic
	}
	t := &ModeTracker{mode: m, logf: logf}
	t.logf("retrieval: %s", startupMessage(m))
	return t
}

// Mode returns the current retrieval mode.
func (t *ModeTracker) Mode() RetrievalMode {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.mode
}

// Set transitions to a new mode, logging only on an actual change (no log spam
// when a healthy embedder keeps succeeding). Returns true if the mode changed.
func (t *ModeTracker) Set(m RetrievalMode) bool {
	t.mu.Lock()
	changed := t.mode != m
	if changed {
		t.mode = m
	}
	t.mu.Unlock()
	if changed {
		t.logf("retrieval: %s", transitionMessage(m))
	}
	return changed
}

// MarkHealthy is called after a successful embed: it clears a prior degraded state
// back to semantic. A no-op in keyword-only mode (no embedder to be healthy).
func (t *ModeTracker) MarkHealthy() {
	t.mu.Lock()
	if t.mode == ModeKeywordOnly {
		t.mu.Unlock()
		return
	}
	t.mu.Unlock()
	t.Set(ModeSemantic)
}

// MarkDegraded is called after an embed failure while an embedder is configured:
// it flips semantic → degraded (FTS5 keeps serving). A no-op in keyword-only mode.
func (t *ModeTracker) MarkDegraded() {
	t.mu.Lock()
	if t.mode == ModeKeywordOnly {
		t.mu.Unlock()
		return
	}
	t.mu.Unlock()
	t.Set(ModeDegraded)
}

func startupMessage(m RetrievalMode) string {
	switch m {
	case ModeKeywordOnly:
		return "keyword-only (FTS5) — no embedder found; install ollama for semantic recall"
	case ModeDegraded:
		return "degraded — embedder configured but unavailable, falling back to FTS5"
	default:
		return "semantic — embedder healthy; vector + FTS5 hybrid"
	}
}

func transitionMessage(m RetrievalMode) string {
	switch m {
	case ModeKeywordOnly:
		return "keyword-only (FTS5) — no embedder configured"
	case ModeDegraded:
		return "degraded — embedder failing, falling back to FTS5; stale chunks will retry"
	default:
		return "semantic — embedder recovered; vector + FTS5 hybrid"
	}
}
