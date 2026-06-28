function nearestBlock(node: Node | null): HTMLElement | null {
  let el = node instanceof HTMLElement ? node : node?.parentElement ?? null;
  while (el && !el.hasAttribute("data-pos-start")) el = el.parentElement;
  return el;
}

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
