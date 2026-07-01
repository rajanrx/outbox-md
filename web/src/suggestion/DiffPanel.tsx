import { useEffect, useMemo, useState } from "react";
import { getSuggestion, accept, rejectSuggestion, type Suggestion } from "../api";
import { unifiedDiff } from "./diff";
import { DiffRows } from "./DiffRows";
import "./diff.css";

// An inline, GitHub-style suggested change shown within a comment thread.
export function DiffPanel({ commentId, currentContent, onDone }: {
  commentId: string;
  currentContent: string;
  onDone: () => void;
}) {
  const [sg, setSg] = useState<Suggestion | null>(null);
  const [busy, setBusy] = useState(false);
  useEffect(() => { getSuggestion(commentId).then(setSg); }, [commentId]);

  const rows = useMemo(
    () => (sg ? unifiedDiff(currentContent, sg.proposedContent) : []),
    [sg, currentContent],
  );
  if (!sg) return null;

  const changed = rows.some((r) => r.op === "ins" || r.op === "del");
  const act = async (fn: () => Promise<unknown>) => { setBusy(true); try { await fn(); onDone(); } finally { setBusy(false); } };

  return (
    <div className="suggestion" onClick={(e) => e.stopPropagation()}>
      <div className="suggestion-head">Suggested change</div>
      {changed ? (
        <DiffRows rows={rows} />
      ) : (
        <div className="diff-empty">No textual changes from the current version.</div>
      )}
      {sg.state === "proposed" ? (
        <div className="diff-actions">
          <button className="primary" disabled={busy} onClick={() => act(() => accept(commentId))}>Approve</button>
          <button disabled={busy} onClick={() => act(() => rejectSuggestion(commentId))}>Reject</button>
        </div>
      ) : (
        <div className="suggestion-state">{sg.state}</div>
      )}
    </div>
  );
}
