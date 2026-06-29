import { unifiedDiff } from "../suggestion/diff";
import "./governance.css";

export function BaselineDiff({ baseline, current, onClose }: {
  baseline: string;
  current: string;
  onClose: () => void;
}) {
  const rows = unifiedDiff(baseline, current);
  const changed = rows.some((r) => r.op === "ins" || r.op === "del");
  const sign = { eq: " ", ins: "+", del: "−", gap: "" } as const;
  return (
    <div className="baseline-diff">
      <div className="baseline-head">
        <span>Pending amendment vs approved baseline</span>
        <button className="baseline-close" onClick={onClose} aria-label="Close">×</button>
      </div>
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
        <div className="diff-empty">No changes ahead of the baseline yet.</div>
      )}
    </div>
  );
}
