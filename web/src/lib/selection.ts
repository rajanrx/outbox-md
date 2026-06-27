export type Anchor = { start: number; end: number };

function toRuneOffset(text: string, jsIndex: number): number {
  return [...text.slice(0, jsIndex)].length;
}

export function selectionToAnchor(text: string, from: number, to: number): Anchor {
  return { start: toRuneOffset(text, from), end: toRuneOffset(text, to) };
}
