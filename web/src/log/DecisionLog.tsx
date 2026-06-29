import { useEffect, useState } from "react";
import { getLog, type LogEntry } from "../api";
import "./decisionlog.css";

const ICON: Record<LogEntry["kind"], string> = {
  created: "✦", comment: "💬", proposal: "✎", edit: "✦", approval: "✓",
};

function phrase(e: LogEntry): string {
  switch (e.kind) {
    case "created": return "created the document (v1)";
    case "comment": return e.detail ? `commented on “${e.detail}”` : "added a comment";
    case "proposal": return "proposed a change";
    case "edit": return `applied an edit → v${e.version}`;
    case "approval": {
      const base = e.reApproval ? `re-approved v${e.version}` : `approved v${e.version}`;
      return e.detail ? `${base} — “${e.detail}”` : base;
    }
  }
}

export function DecisionLog({ docId, onClose }: { docId: string; onClose: () => void }) {
  const [entries, setEntries] = useState<LogEntry[]>([]);
  useEffect(() => { getLog(docId).then((e) => setEntries(e ?? [])); }, [docId]);

  return (
    <div className="log-panel" onClick={(e) => e.stopPropagation()}>
      <div className="log-head">
        <span>History</span>
        <button className="log-close" onClick={onClose} aria-label="Close">×</button>
      </div>
      {entries.length === 0 ? (
        <div className="log-empty">No activity yet.</div>
      ) : (
        <ol className="log-list">
          {entries.map((e, i) => (
            <li key={i} className={`log-row kind-${e.kind}`}>
              <span className="log-ic" aria-hidden>{ICON[e.kind]}</span>
              <div className="log-body">
                <span className={`log-actor who-${e.actor}`}>{e.actor}</span> {phrase(e)}
                <div className="log-time">{e.time}</div>
              </div>
            </li>
          ))}
        </ol>
      )}
    </div>
  );
}
