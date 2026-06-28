import { useEffect, useMemo, useState } from "react";
import { diff_match_patch } from "diff-match-patch";
import { getSuggestion, accept, rejectSuggestion, type Suggestion } from "../api";
import "./diff.css";

const dmp = new diff_match_patch();
const CTX = 3; // context lines kept around each change

type Row = { op: "eq" | "ins" | "del" | "gap"; text: string };

// A unified, line-based diff: only changed lines plus a few lines of context,
// long unchanged runs collapsed to a "… N unchanged lines" marker.
function unifiedDiff(before: string, after: string): Row[] {
  const a = (dmp as any).diff_linesToChars_(before, after);
  const diffs = dmp.diff_main(a.chars1, a.chars2, false);
  (dmp as any).diff_charsToLines_(diffs, a.lineArray);

  const lines: Row[] = [];
  for (const [op, chunk] of diffs) {
    const parts = chunk.split("\n");
    if (parts.length && parts[parts.length - 1] === "") parts.pop();
    const kind = op === 1 ? "ins" : op === -1 ? "del" : "eq";
    for (const p of parts) lines.push({ op: kind, text: p });
  }

  const rows: Row[] = [];
  let i = 0;
  while (i < lines.length) {
    if (lines[i].op !== "eq") { rows.push(lines[i]); i++; continue; }
    let j = i;
    while (j < lines.length && lines[j].op === "eq") j++;
    const runLen = j - i;
    const showStart = i === 0 ? 0 : CTX;          // trailing context of change above
    const showEnd = j === lines.length ? 0 : CTX; // leading context of change below
    if (showStart + showEnd >= runLen) {
      for (let k = i; k < j; k++) rows.push(lines[k]);
    } else {
      for (let k = i; k < i + showStart; k++) rows.push(lines[k]);
      rows.push({ op: "gap", text: `… ${runLen - showStart - showEnd} unchanged lines` });
      for (let k = j - showEnd; k < j; k++) rows.push(lines[k]);
    }
    i = j;
  }
  return rows;
}

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
  const sign = { eq: " ", ins: "+", del: "−", gap: "" } as const;
  const act = async (fn: () => Promise<unknown>) => { setBusy(true); try { await fn(); onDone(); } finally { setBusy(false); } };

  return (
    <div className="suggestion" onClick={(e) => e.stopPropagation()}>
      <div className="suggestion-head">Suggested change</div>
      {changed ? (
        <div className="diff">
          {rows.map((r, i) => (
            <div key={i} className={`drow ${r.op}`}>
              <span className="sign">{sign[r.op]}</span>
              <span className="text">{r.text || " "}</span>
            </div>
          ))}
        </div>
      ) : (
        <div className="diff-empty">No textual changes from the current version.</div>
      )}
      {sg.state === "proposed" ? (
        <div className="diff-actions">
          <button className="primary" disabled={busy} onClick={() => act(() => accept(commentId))}>Accept</button>
          <button disabled={busy} onClick={() => act(() => rejectSuggestion(commentId))}>Reject</button>
        </div>
      ) : (
        <div className="suggestion-state">{sg.state}</div>
      )}
    </div>
  );
}
