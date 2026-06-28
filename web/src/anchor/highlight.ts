import { diff_match_patch } from "diff-match-patch";

const dmp = new diff_match_patch();

type Anchor = { start: number; end: number };
type CommentLike = { id: string; anchor: Anchor };

function runeToUtf16(s: string, runeIdx: number): number {
  return [...s].slice(0, runeIdx).join("").length;
}
function utf16ToRune(s: string, utf16: number): number {
  return [...s.slice(0, utf16)].length;
}

// xIndex maps a location in text1 → text2.
function mapOffset(text1: string, text2: string, off: number): number {
  const diffs = dmp.diff_main(text1, text2);
  dmp.diff_cleanupSemantic(diffs);
  return dmp.diff_xIndex(diffs, off);
}

// Rendered-text offset of (node, offset) within block.textContent.
function offsetInBlock(block: HTMLElement, node: Node, offset: number): number {
  let total = 0;
  const walker = document.createTreeWalker(block, NodeFilter.SHOW_TEXT);
  let n: Node | null;
  while ((n = walker.nextNode())) {
    if (n === node) return total + offset;
    total += n.textContent?.length ?? 0;
  }
  return total;
}

// Smallest stamped block whose source span contains [aS,aE] (UTF-16 offsets).
function blockFor(root: HTMLElement, aS: number, aE: number): HTMLElement | null {
  let best: HTMLElement | null = null;
  let bestSize = Infinity;
  root.querySelectorAll<HTMLElement>("[data-pos-start]").forEach((el) => {
    const s = Number(el.getAttribute("data-pos-start"));
    const e = Number(el.getAttribute("data-pos-end"));
    if (aS >= s && aE <= e && e - s < bestSize) { best = el; bestSize = e - s; }
  });
  return best;
}

function isWholeBlock(block: HTMLElement): boolean {
  return (
    block.hasAttribute("data-mermaid") ||
    block.querySelector("[data-mermaid]") != null ||
    !(block.textContent ?? "").trim()
  );
}

// DOM Range for [rStart,rEnd) measured in block.textContent (UTF-16).
function rangeFromRendered(block: HTMLElement, rStart: number, rEnd: number): Range | null {
  const walker = document.createTreeWalker(block, NodeFilter.SHOW_TEXT);
  let acc = 0;
  let sn: Node | null = null, so = 0, en: Node | null = null, eo = 0;
  let n: Node | null;
  while ((n = walker.nextNode())) {
    const len = n.textContent?.length ?? 0;
    if (sn === null && rStart <= acc + len) { sn = n; so = rStart - acc; }
    if (en === null && rEnd <= acc + len) { en = n; eo = rEnd - acc; }
    acc += len;
    if (sn && en) break;
  }
  if (!sn || !en) return null;
  const r = document.createRange();
  try { r.setStart(sn, so); r.setEnd(en, eo); } catch { return null; }
  return r;
}

// A precise DOM Range for an anchor (rune offsets into source).
export function rangeForAnchor(root: HTMLElement, source: string, anchor: Anchor): Range | null {
  const aS = runeToUtf16(source, anchor.start);
  const aE = runeToUtf16(source, anchor.end);
  const block = blockFor(root, aS, aE);
  if (!block) return null;
  if (isWholeBlock(block)) {
    const r = document.createRange();
    try { r.selectNodeContents(block); return r; } catch { return null; }
  }
  const dps = Number(block.getAttribute("data-pos-start"));
  const blockSource = source.slice(dps, Number(block.getAttribute("data-pos-end")));
  const rendered = block.textContent ?? "";
  const rS = mapOffset(blockSource, rendered, aS - dps);
  const rE = mapOffset(blockSource, rendered, aE - dps);
  return rangeFromRendered(block, rS, Math.max(rE, rS));
}

// The comment whose anchor covers a click point, if any.
export function commentAtPoint(
  root: HTMLElement,
  source: string,
  comments: CommentLike[],
  x: number,
  y: number,
): string | null {
  const d = document as any;
  let node: Node | null = null;
  let offset = 0;
  if (d.caretPositionFromPoint) {
    const p = d.caretPositionFromPoint(x, y);
    if (p) { node = p.offsetNode; offset = p.offset; }
  } else if (d.caretRangeFromPoint) {
    const r = d.caretRangeFromPoint(x, y);
    if (r) { node = r.startContainer; offset = r.startOffset; }
  }
  if (!node) return null;
  let el: HTMLElement | null = node instanceof HTMLElement ? node : node.parentElement;
  while (el && !el.hasAttribute("data-pos-start")) el = el.parentElement;
  if (!el || !root.contains(el)) return null;

  const dps = Number(el.getAttribute("data-pos-start"));
  let srcU16: number;
  if (isWholeBlock(el)) {
    srcU16 = dps;
  } else {
    const rOff = offsetInBlock(el, node, offset);
    const blockSource = source.slice(dps, Number(el.getAttribute("data-pos-end")));
    srcU16 = dps + mapOffset(el.textContent ?? "", blockSource, rOff);
  }
  const srcRune = utf16ToRune(source, srcU16);

  // Prefer the tightest matching anchor.
  let hit: string | null = null;
  let span = Infinity;
  for (const c of comments) {
    if (srcRune >= c.anchor.start && srcRune <= c.anchor.end && c.anchor.end - c.anchor.start <= span) {
      hit = c.id; span = c.anchor.end - c.anchor.start;
    }
  }
  return hit;
}
