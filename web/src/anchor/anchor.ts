import { blockTextOffsets } from "./selection";
import { mapRenderedToSource } from "./map";

// UTF-16 string index → rune (code-point) offset, matching the Go backend's []rune model.
function toRune(s: string, utf16Index: number): number {
  return [...s.slice(0, utf16Index)].length;
}

export function computeAnchor(
  source: string,
  root: HTMLElement,
  range: Range,
): { start: number; end: number } | null {
  const sel = blockTextOffsets(root, range);
  if (!sel) return null;
  const psAttr = sel.blockEl.getAttribute("data-pos-start");
  const peAttr = sel.blockEl.getAttribute("data-pos-end");
  if (psAttr == null || peAttr == null) return null;
  const ps = Number(psAttr);
  const pe = Number(peAttr);
  if (Number.isNaN(ps) || Number.isNaN(pe)) return null;

  const rendered = sel.blockEl.textContent ?? "";
  let raw: { start: number; end: number };
  // Non-text blocks (Mermaid/images) or a block containing a rendered Mermaid
  // diagram: anchor the whole block. querySelector covers the production case
  // where MermaidBlock's <div data-mermaid> sits inside a stamped <pre>.
  if (
    sel.blockEl.hasAttribute("data-mermaid") ||
    sel.blockEl.querySelector("[data-mermaid]") ||
    !rendered.trim()
  ) {
    raw = { start: ps, end: pe };
  } else {
    const m = mapRenderedToSource(source.slice(ps, pe), rendered, sel.rStart, sel.rEnd);
    raw = { start: ps + m.start, end: ps + m.end };
  }
  return { start: toRune(source, raw.start), end: toRune(source, raw.end) };
}
