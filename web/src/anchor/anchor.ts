import { blockTextOffsets } from "./selection";
import { mapRenderedToSource } from "./map";

function nearestBlock(node: Node | null): HTMLElement | null {
  let el = node instanceof HTMLElement ? node : node?.parentElement ?? null;
  while (el && !el.hasAttribute("data-pos-start")) el = el.parentElement;
  return el;
}

function wholeBlock(
  block: HTMLElement,
): { start: number; end: number } | null {
  const ps = Number(block.getAttribute("data-pos-start"));
  const pe = Number(block.getAttribute("data-pos-end"));
  if (Number.isNaN(ps) || Number.isNaN(pe)) return null;
  return { start: ps, end: pe };
}

export function computeAnchor(
  source: string,
  root: HTMLElement,
  range: Range,
): { start: number; end: number } | null {
  const sel = blockTextOffsets(root, range);
  if (!sel) {
    // blockTextOffsets returns null for selections with no text content (e.g.
    // a Mermaid block whose only child is an <svg>). Treat a non-collapsed
    // selection whose nearest block is non-text / Mermaid as a whole-block
    // anchor.
    if (range.collapsed) return null;
    const block = nearestBlock(range.startContainer);
    if (!block || nearestBlock(range.endContainer) !== block) return null;
    if (!root.contains(block)) return null;
    if (block.hasAttribute("data-mermaid") || !(block.textContent ?? "").trim()) {
      return wholeBlock(block);
    }
    return null;
  }
  const rendered = sel.blockEl.textContent ?? "";
  // Non-text / Mermaid / empty-text blocks: anchor the whole block.
  if (sel.blockEl.hasAttribute("data-mermaid") || !rendered.trim()) {
    return wholeBlock(sel.blockEl);
  }
  const whole = wholeBlock(sel.blockEl);
  if (!whole) return null;
  const m = mapRenderedToSource(source.slice(whole.start, whole.end), rendered, sel.rStart, sel.rEnd);
  return { start: whole.start + m.start, end: whole.start + m.end };
}
