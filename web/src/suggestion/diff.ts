import { diff_match_patch } from "diff-match-patch";

const dmp = new diff_match_patch();
const CTX = 3; // context lines kept around each change

// ── word/char segments within a single line ──────────────────────────────────
// A Segment is a run of characters tagged eq/ins/del. Only *replaced* line pairs
// carry ins/del segments (the darker intra-line highlights); equal, pure-insert
// and pure-delete lines carry a single eq segment, so the DARK word highlight
// only ever appears where a real word-level diff ran. The light per-line
// background comes from the cell/row class, not the segments.
export type SegOp = "eq" | "ins" | "del";
export type Segment = { op: SegOp; text: string };

// A rendered line on one side of the diff: its 1-based line number plus the
// word-level segments that make up its text.
export type LineCell = { num: number; segs: Segment[] };

// AlignedRow drives the side-by-side (split) view and is the source of truth the
// inline view is flattened from.
//  equal   → same line on both sides
//  insert  → new line only (left cell empty)
//  delete  → removed line only (right cell empty)
//  replace → old line beside new line, both word-highlighted
//  gap     → a collapsed "… N unchanged lines" row spanning both columns
export type AlignedType = "equal" | "insert" | "delete" | "replace" | "gap";
export type AlignedRow = {
  type: AlignedType;
  left?: LineCell; // absent for insert
  right?: LineCell; // absent for delete
  count?: number; // gap only: number of collapsed unchanged lines
};

// Backwards-compatible unified row. `text` is kept for the compact excerpt logic
// and counts(); `segs` (optional) adds word-level highlighting when present.
export type Row = { op: "eq" | "ins" | "del" | "gap"; text: string; segs?: Segment[]; num?: number };

// wordDiff runs a character-level diff over two line strings and cleans it up to
// human word boundaries, returning eq/ins/del segments (ins = present only in
// `after`, del = present only in `before`).
export function wordDiff(before: string, after: string): Segment[] {
  const d = dmp.diff_main(before, after);
  dmp.diff_cleanupSemantic(d);
  return d.map(([op, text]) => ({ op: op === 1 ? "ins" : op === -1 ? "del" : "eq", text }));
}

// splitLines runs a line-level diff (via diff-match-patch's line-to-char trick)
// and returns a flat list of {op, text} lines, op ∈ eq|ins|del.
type FlatLine = { op: "eq" | "ins" | "del"; text: string };
function splitLines(before: string, after: string): FlatLine[] {
  const a = (dmp as any).diff_linesToChars_(before, after);
  const diffs = dmp.diff_main(a.chars1, a.chars2, false);
  (dmp as any).diff_charsToLines_(diffs, a.lineArray);

  const out: FlatLine[] = [];
  for (const [op, chunk] of diffs) {
    const parts = chunk.split("\n");
    if (parts.length && parts[parts.length - 1] === "") parts.pop();
    const kind = op === 1 ? "ins" : op === -1 ? "del" : "eq";
    for (const p of parts) out.push({ op: kind, text: p });
  }
  return out;
}

// buildAligned turns the flat line list into aligned rows, pairing each block of
// removed lines with the following block of added lines index-by-index into
// "replace" rows (leftover removals → delete, leftover additions → insert). A
// block is gathered as one contiguous run of non-equal lines then split by op,
// so it is robust to either delete-first or insert-first emit order.
function buildAligned(lines: FlatLine[]): AlignedRow[] {
  const out: AlignedRow[] = [];
  let li = 0; // left (old) line number
  let ri = 0; // right (new) line number
  let k = 0;
  while (k < lines.length) {
    if (lines[k].op === "eq") {
      li++;
      ri++;
      const text = lines[k].text;
      out.push({
        type: "equal",
        left: { num: li, segs: [{ op: "eq", text }] },
        right: { num: ri, segs: [{ op: "eq", text }] },
      });
      k++;
      continue;
    }
    const dels: string[] = [];
    const inss: string[] = [];
    while (k < lines.length && lines[k].op !== "eq") {
      if (lines[k].op === "del") dels.push(lines[k].text);
      else inss.push(lines[k].text);
      k++;
    }
    const pairs = Math.min(dels.length, inss.length);
    for (let p = 0; p < pairs; p++) {
      li++;
      ri++;
      const segs = wordDiff(dels[p], inss[p]);
      out.push({
        type: "replace",
        left: { num: li, segs: segs.filter((s) => s.op !== "ins") },
        right: { num: ri, segs: segs.filter((s) => s.op !== "del") },
      });
    }
    for (let p = pairs; p < dels.length; p++) {
      li++;
      out.push({ type: "delete", left: { num: li, segs: [{ op: "eq", text: dels[p] }] } });
    }
    for (let p = pairs; p < inss.length; p++) {
      ri++;
      out.push({ type: "insert", right: { num: ri, segs: [{ op: "eq", text: inss[p] }] } });
    }
  }
  return out;
}

// collapseGaps replaces long runs of equal rows with a single gap row, keeping
// CTX lines of context on each side (mirrors the inline view's behaviour).
function collapseGaps(rows: AlignedRow[]): AlignedRow[] {
  const out: AlignedRow[] = [];
  let i = 0;
  while (i < rows.length) {
    if (rows[i].type !== "equal") {
      out.push(rows[i]);
      i++;
      continue;
    }
    let j = i;
    while (j < rows.length && rows[j].type === "equal") j++;
    const runLen = j - i;
    const showStart = i === 0 ? 0 : CTX; // trailing context of change above
    const showEnd = j === rows.length ? 0 : CTX; // leading context of change below
    if (showStart + showEnd >= runLen) {
      for (let k = i; k < j; k++) out.push(rows[k]);
    } else {
      for (let k = i; k < i + showStart; k++) out.push(rows[k]);
      out.push({ type: "gap", count: runLen - showStart - showEnd });
      for (let k = j - showEnd; k < j; k++) out.push(rows[k]);
    }
    i = j;
  }
  return out;
}

// alignedDiff is the model for the side-by-side view: gap-collapsed aligned rows
// with word-level segments on replaced line pairs.
export function alignedDiff(before: string, after: string): AlignedRow[] {
  return collapseGaps(buildAligned(splitLines(before, after)));
}

// unifiedDiff is the inline (single-column) model, derived from the same aligned
// rows so the two views share pairing + word segmentation. Change blocks are
// flattened GitHub-style — all removed (−) lines first, then all added (+) lines
// — rather than interleaved, which also keeps the compact card excerpt stable.
export function unifiedDiff(before: string, after: string): Row[] {
  const aligned = alignedDiff(before, after);
  const rows: Row[] = [];
  let i = 0;
  while (i < aligned.length) {
    const r = aligned[i];
    if (r.type === "equal") {
      rows.push({ op: "eq", text: r.left!.segs.map((s) => s.text).join(""), segs: r.left!.segs, num: r.left!.num });
      i++;
      continue;
    }
    if (r.type === "gap") {
      rows.push({ op: "gap", text: `… ${r.count} unchanged lines` });
      i++;
      continue;
    }
    // Gather a contiguous change block, emit all left (−) sides then all right (+) sides.
    const block: AlignedRow[] = [];
    while (i < aligned.length && (aligned[i].type === "delete" || aligned[i].type === "insert" || aligned[i].type === "replace")) {
      block.push(aligned[i]);
      i++;
    }
    for (const b of block) {
      if (b.left) rows.push({ op: "del", text: b.left.segs.map((s) => s.text).join(""), segs: b.left.segs, num: b.left.num });
    }
    for (const b of block) {
      if (b.right) rows.push({ op: "ins", text: b.right.segs.map((s) => s.text).join(""), segs: b.right.segs, num: b.right.num });
    }
  }
  return rows;
}
