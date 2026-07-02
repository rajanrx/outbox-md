import { type AlignedRow, type LineCell } from "./diff";
import { DiffSegs } from "./DiffRows";
import { type LineRef } from "./refine";
import { AddCommentButton, hasLineContent, LineCommentZone, type LineCommentApi } from "./LineComments";

const cellText = (c: LineCell) => c.segs.map((s) => s.text).join("");

// DiffSplit renders the side-by-side (GitHub "split") view. The outer .diff-split
// is a 4-column CSS grid (old-num | old-line | new-num | new-line); every row is
// `display: contents` so its four cells land directly in the shared grid, which
// makes the two sides auto-equalise height per row — long lines wrap without
// breaking column alignment. A gap row is a single cell spanning all 4 columns.
//
// Cell backgrounds:
//  equal   → both sides plain
//  delete  → left light-red, right empty
//  insert  → left empty,     right light-green
//  replace → left light-red (with dark del words), right light-green (dark ins)
//
// When `lineComments` is supplied (the modal's live proposed suggestion) each
// number gutter gains a hover "＋" and any line with drafts/an open editor gets a
// full-width comment row (grid-column 1/-1, like the gap row) beneath it. Equal
// (context) lines are commentable only from the OLD gutter so their key matches
// the inline view; changed lines expose whichever side(s) they touch.
export function DiffSplit({ rows, lineComments }: { rows: AlignedRow[]; lineComments?: LineCommentApi }) {
  return (
    <div className="diff diff-split">
      {rows.map((r, i) => {
        if (r.type === "gap") {
          return (
            <div key={i} className="drow-split gap">
              <div className="dcell gap-cell">… {r.count} unchanged lines</div>
            </div>
          );
        }
        const leftKind = r.type === "delete" || r.type === "replace" ? "del" : r.left ? "eq" : "empty";
        const rightKind = r.type === "insert" || r.type === "replace" ? "ins" : r.right ? "eq" : "empty";
        // Old side is commentable on any row that has a left line (delete/replace/
        // equal). New side only on lines the suggestion adds (insert/replace) — an
        // equal line's new gutter is suppressed so it doesn't double up with the
        // old-keyed context comment.
        const oldRef: LineRef | null =
          lineComments && r.left ? { side: "old", lineNo: r.left.num, snippet: cellText(r.left) } : null;
        const newRef: LineRef | null =
          lineComments && r.right && (r.type === "insert" || r.type === "replace")
            ? { side: "new", lineNo: r.right.num, snippet: cellText(r.right) }
            : null;
        const showZone =
          !!lineComments &&
          ((oldRef && hasLineContent(lineComments, oldRef)) || (newRef && hasLineContent(lineComments, newRef)));
        return (
          <div key={i} className="drow-split">
            <div className="dnum dnum-l" aria-hidden="true">
              {oldRef && lineComments && <AddCommentButton api={lineComments} lineRef={oldRef} />}
              {r.left?.num ?? ""}
            </div>
            <div className={`dcell left ${leftKind}`}>
              {r.left ? <DiffSegs segs={r.left.segs} /> : <span className="ph" />}
            </div>
            <div className="dnum dnum-r" aria-hidden="true">
              {newRef && lineComments && <AddCommentButton api={lineComments} lineRef={newRef} />}
              {r.right?.num ?? ""}
            </div>
            <div className={`dcell right ${rightKind}`}>
              {r.right ? <DiffSegs segs={r.right.segs} /> : <span className="ph" />}
            </div>
            {showZone && lineComments && (
              <div className="drow-split-zone">
                <div className="line-comment-cell">
                  {oldRef && <LineCommentZone api={lineComments} lineRef={oldRef} />}
                  {newRef && <LineCommentZone api={lineComments} lineRef={newRef} />}
                </div>
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
}
