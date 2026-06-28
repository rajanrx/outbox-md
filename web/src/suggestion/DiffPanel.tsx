import { useEffect, useState } from "react";
import { diff_match_patch } from "diff-match-patch";
import { getSuggestion, accept, rejectSuggestion, type Suggestion } from "../api";
import "./diff.css";

const dmp = new diff_match_patch();

export function DiffPanel({ commentId, currentContent, onDone }: {
  commentId: string;
  currentContent: string;
  onDone: () => void;
}) {
  const [sg, setSg] = useState<Suggestion | null>(null);
  useEffect(() => { getSuggestion(commentId).then(setSg); }, [commentId]);
  if (!sg) return null;

  const diffs = dmp.diff_main(currentContent, sg.proposedContent);
  dmp.diff_cleanupSemantic(diffs);

  return (
    <div className="diff-overlay" onClick={onDone}>
      <div className="diff-panel" onClick={(e) => e.stopPropagation()}>
        <h4>Proposed change</h4>
        <pre className="diff">
          {diffs.map(([op, text], i) => (
            <span key={i} className={op === 1 ? "ins" : op === -1 ? "del" : "eq"}>{text}</span>
          ))}
        </pre>
        <div className="diff-actions">
          <button disabled={sg.state !== "proposed"} onClick={async () => { await accept(commentId); onDone(); }}>Accept</button>
          <button disabled={sg.state !== "proposed"} onClick={async () => { await rejectSuggestion(commentId); onDone(); }}>Reject</button>
          <button onClick={onDone}>Close</button>
        </div>
      </div>
    </div>
  );
}
