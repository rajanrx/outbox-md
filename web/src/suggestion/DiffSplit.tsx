import { type AlignedRow } from "./diff";
import { DiffSegs } from "./DiffRows";

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
export function DiffSplit({ rows }: { rows: AlignedRow[] }) {
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
        return (
          <div key={i} className="drow-split">
            <div className="dnum dnum-l" aria-hidden="true">{r.left?.num ?? ""}</div>
            <div className={`dcell left ${leftKind}`}>
              {r.left ? <DiffSegs segs={r.left.segs} /> : <span className="ph" />}
            </div>
            <div className="dnum dnum-r" aria-hidden="true">{r.right?.num ?? ""}</div>
            <div className={`dcell right ${rightKind}`}>
              {r.right ? <DiffSegs segs={r.right.segs} /> : <span className="ph" />}
            </div>
          </div>
        );
      })}
    </div>
  );
}
