import { type Row, type Segment } from "./diff";

const SIGN = { eq: " ", ins: "+", del: "−", gap: "" } as const;

// DiffSegs renders a line's word-level segments. `eq` runs are plain text; `ins`
// and `del` runs get a darker highlight span (`seg ins` / `seg del`). Only
// replaced line pairs carry ins/del segments, so the dark highlight appears only
// on the specific changed words — the light per-line background is supplied by
// the row/cell class, not here.
export function DiffSegs({ segs, fallback }: { segs?: Segment[]; fallback?: string }) {
  if (!segs || segs.length === 0) return <>{fallback || " "}</>;
  return (
    <>
      {segs.map((s, i) =>
        s.op === "eq" ? (
          <span key={i}>{s.text}</span>
        ) : (
          <span key={i} className={`seg ${s.op}`}>{s.text}</span>
        ),
      )}
    </>
  );
}

// DiffRows renders the inline (unified, single-column) view: one row per line
// with a +/−/space sign gutter and word-level highlights inside changed lines.
// It is shared by the inline suggestion excerpt in the comment card and the
// modal's inline mode.
export function DiffRows({ rows }: { rows: Row[] }) {
  return (
    <div className="diff diff-inline">
      {rows.map((r, i) => (
        <div key={i} className={`drow ${r.op}`}>
          <span className="sign">{SIGN[r.op]}</span>
          <span className="text">
            {r.op === "gap" ? r.text : <DiffSegs segs={r.segs} fallback={r.text} />}
          </span>
        </div>
      ))}
    </div>
  );
}

// counts tallies inserted/deleted lines for a `+N −M` summary badge.
export function counts(rows: Row[]): { ins: number; del: number } {
  let ins = 0;
  let del = 0;
  for (const r of rows) {
    if (r.op === "ins") ins++;
    else if (r.op === "del") del++;
  }
  return { ins, del };
}
