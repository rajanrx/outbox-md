import { useCallback, useMemo, useState } from "react";
import { alignedDiff, unifiedDiff } from "./diff";
import { DiffRows } from "./DiffRows";
import { DiffSplit } from "./DiffSplit";
import { type LineCommentApi } from "./LineComments";

export type DiffViewMode = "split" | "inline" | "rendered";

const LS_KEY = "outbox-diff-view";
const DEFAULT: DiffViewMode = "split"; // GitHub-style side-by-side by default

// useDiffView holds the chosen view mode, persisted to localStorage so the
// preference sticks across suggestions and sessions.
export function useDiffView(): [DiffViewMode, (m: DiffViewMode) => void] {
  const [mode, setMode] = useState<DiffViewMode>(() => {
    try {
      const v = localStorage.getItem(LS_KEY);
      return v === "inline" || v === "split" || v === "rendered" ? v : DEFAULT;
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
      <button
        role="tab"
        aria-selected={mode === "rendered"}
        className={mode === "rendered" ? "on" : ""}
        onClick={() => onChange("rendered")}
        title="Preview the proposed doc rendered as Markdown (incl. mermaid)"
      >
        Rendered
      </button>
    </div>
  );
}

// DiffView renders a single before→after diff in the requested mode, computing
// only the model the active view needs (memoised per content + mode). When
// `lineComments` is passed the diff becomes annotatable (hover "＋" + inline
// drafts); it is threaded to whichever view is active so a draft anchored in one
// mode still shows after toggling.
export function DiffView({
  before,
  after,
  mode,
  lineComments,
}: {
  before: string;
  after: string;
  mode: DiffViewMode;
  lineComments?: LineCommentApi;
}) {
  const split = useMemo(() => (mode === "split" ? alignedDiff(before, after) : null), [before, after, mode]);
  const inline = useMemo(() => (mode === "inline" ? unifiedDiff(before, after) : null), [before, after, mode]);
  return mode === "split" ? (
    <DiffSplit rows={split!} lineComments={lineComments} />
  ) : (
    <DiffRows rows={inline!} lineComments={lineComments} />
  );
}
