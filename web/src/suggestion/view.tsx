import { useCallback, useMemo, useState } from "react";
import { alignedDiff, unifiedDiff } from "./diff";
import { DiffRows } from "./DiffRows";
import { DiffSplit } from "./DiffSplit";

export type DiffViewMode = "split" | "inline";

const LS_KEY = "outbox-diff-view";
const DEFAULT: DiffViewMode = "split"; // GitHub-style side-by-side by default

// useDiffView holds the chosen view mode, persisted to localStorage so the
// preference sticks across suggestions and sessions.
export function useDiffView(): [DiffViewMode, (m: DiffViewMode) => void] {
  const [mode, setMode] = useState<DiffViewMode>(() => {
    try {
      const v = localStorage.getItem(LS_KEY);
      return v === "inline" || v === "split" ? v : DEFAULT;
    } catch {
      return DEFAULT;
    }
  });
  const set = useCallback((m: DiffViewMode) => {
    setMode(m);
    try {
      localStorage.setItem(LS_KEY, m);
    } catch {
      /* private mode / storage disabled — keep in-memory only */
    }
  }, []);
  return [mode, set];
}

// DiffToggle is the segmented "Side by side | Inline" control for the modal header.
export function DiffToggle({ mode, onChange }: { mode: DiffViewMode; onChange: (m: DiffViewMode) => void }) {
  return (
    <div className="diff-toggle" role="tablist" aria-label="Diff view">
      <button
        role="tab"
        aria-selected={mode === "split"}
        className={mode === "split" ? "on" : ""}
        onClick={() => onChange("split")}
      >
        Side by side
      </button>
      <button
        role="tab"
        aria-selected={mode === "inline"}
        className={mode === "inline" ? "on" : ""}
        onClick={() => onChange("inline")}
      >
        Inline
      </button>
    </div>
  );
}

// DiffView renders a single before→after diff in the requested mode, computing
// only the model the active view needs (memoised per content + mode).
export function DiffView({ before, after, mode }: { before: string; after: string; mode: DiffViewMode }) {
  const split = useMemo(() => (mode === "split" ? alignedDiff(before, after) : null), [before, after, mode]);
  const inline = useMemo(() => (mode === "inline" ? unifiedDiff(before, after) : null), [before, after, mode]);
  return mode === "split" ? <DiffSplit rows={split!} /> : <DiffRows rows={inline!} />;
}
