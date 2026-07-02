import { type Row, type Segment } from "./diff";
import { type LineRef } from "./refine";
import { AddCommentButton, LineCommentZone, type LineCommentApi } from "./LineComments";

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

// lineRefFor derives the inline row's comment anchor: an added line refers to the
// new side, everything else (equal/removed) to the old side — matching the
// split view's keys so a draft stays attached when the view is toggled. Gap rows
// and rows without a line number are not commentable.
function lineRefFor(r: Row): LineRef | null {
  if (r.op === "gap" || r.num == null) return null;
  return { side: r.op === "ins" ? "new" : "old", lineNo: r.num, snippet: r.text };
}

// DiffRows renders the inline (unified, single-column) view: one row per line
// with a +/−/space sign gutter and word-level highlights inside changed lines.
// It is shared by the inline suggestion excerpt in the comment card and the
// modal's inline mode. When `lineComments` is supplied (the modal's live proposed
// suggestion), each commentable row gains a hover "＋" affordance and renders its
// drafts + editor beneath it; without it the diff is unchanged.
export function DiffRows({ rows, lineComments }: { rows: Row[]; lineComments?: LineCommentApi }) {
  return (
    <div className="diff diff-inline">
      {rows.map((r, i) => {
        const ref = lineComments ? lineRefFor(r) : null;
        return (
          <div key={i} className="drow-line">
            <div className={`drow ${r.op}` + (ref ? " commentable" : "")}>
              {ref && lineComments && <AddCommentButton api={lineComments} lineRef={ref} />}
              <span className="sign">{SIGN[r.op]}</span>
              <span className="text">
                {r.op === "gap" ? r.text : <DiffSegs segs={r.segs} fallback={r.text} />}
              </span>
            </div>
            {ref && lineComments && <LineCommentZone api={lineComments} lineRef={ref} />}
          </div>
        );
      })}
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
