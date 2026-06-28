function nearestBlock(node: Node | null): HTMLElement | null {
  let el = node instanceof HTMLElement ? node : node?.parentElement ?? null;
  while (el && !el.hasAttribute("data-pos-start")) el = el.parentElement;
  return el;
}

// Rendered-text offset of (node, offset) within block. Range-based so it works
// for both text-node containers and element containers (e.g. a selection whose
// endpoint sits on an SVG <text> element inside a Mermaid diagram).
function offsetInBlock(block: HTMLElement, node: Node, offset: number): number {
  const r = document.createRange();
  r.selectNodeContents(block);
  r.setEnd(node, offset);
  return r.toString().length;
}

export function blockTextOffsets(
  root: HTMLElement,
  range: Range,
): { blockEl: HTMLElement; rStart: number; rEnd: number } | null {
  if (range.collapsed) return null;
  const startBlock = nearestBlock(range.startContainer);
  const endBlock = nearestBlock(range.endContainer);
  if (!startBlock || startBlock !== endBlock || !root.contains(startBlock)) return null;
  const rStart = offsetInBlock(startBlock, range.startContainer, range.startOffset);
  const rEnd = offsetInBlock(startBlock, range.endContainer, range.endOffset);
  if (rEnd <= rStart) return null;
  return { blockEl: startBlock, rStart, rEnd };
}
