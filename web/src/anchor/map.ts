import { diff_match_patch } from "diff-match-patch";

const dmp = new diff_match_patch();

// Map [rStart,rEnd) in renderedText to source offsets. Rendered text is the
// source with markdown syntax tokens removed, so we diff the two and translate
// each boundary. End maps via the last selected char (+1) so an exclusive end
// does not overshoot trailing syntax.
export function mapRenderedToSource(
  sourceText: string,
  renderedText: string,
  rStart: number,
  rEnd: number,
): { start: number; end: number } {
  const diffs = dmp.diff_main(renderedText, sourceText);
  dmp.diff_cleanupSemantic(diffs);
  const start = dmp.diff_xIndex(diffs, rStart);
  const lastChar = Math.max(rStart, rEnd - 1);
  const end = dmp.diff_xIndex(diffs, lastChar) + 1;
  return { start, end: Math.max(end, start) };
}
