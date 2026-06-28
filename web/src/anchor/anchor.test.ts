// @vitest-environment jsdom
import { expect, test } from "vitest";
import { computeAnchor } from "./anchor";

const runeSlice = (s: string, a: { start: number; end: number }) =>
  [...s].slice(a.start, a.end).join("");

test("prose selection → precise source range across syntax (with nested inline)", () => {
  const source = "para before\n\nThis is **bold** text\n";
  const start = source.indexOf("This");
  const end = source.indexOf(" text");
  // Real react-markdown nests <strong> inside <p>; only <p> is stamped.
  document.body.innerHTML =
    `<div id="root"><p data-pos-start="${start}" data-pos-end="${end}">This is <strong>bold</strong> text</p></div>`;
  const strong = document.querySelector("strong")!;
  const r = document.createRange();
  r.setStart(strong.firstChild!, 0);
  r.setEnd(strong.firstChild!, 4);
  const a = computeAnchor(source, document.getElementById("root")!, r)!;
  expect(runeSlice(source, a)).toBe("bold");
});

test("offsets are rune-based for astral characters", () => {
  const source = "😀 hello world";
  document.body.innerHTML =
    `<div id="root"><p data-pos-start="0" data-pos-end="${source.length}">😀 hello world</p></div>`;
  const p = document.querySelector("p")!;
  const t = p.firstChild!;
  // "world" begins after "😀 hello " — in the DOM text that's UTF-16 index 9.
  const r = document.createRange();
  r.setStart(t, 9);
  r.setEnd(t, 14);
  const a = computeAnchor(source, document.getElementById("root")!, r)!;
  expect(runeSlice(source, a)).toBe("world");
  expect(a.start).toBe(8); // rune offset (emoji is 1 rune), not the UTF-16 index 9
});

test("mermaid block (production DOM: <pre> with inner data-mermaid div) → whole-block", () => {
  const source = "```mermaid\nflowchart LR\nA-->B\n```";
  document.body.innerHTML =
    `<div id="root"><pre data-pos-start="0" data-pos-end="${source.length}"><div data-mermaid><svg><text>A</text></svg></div></pre></div>`;
  const svgText = document.querySelector("text")!;
  const r = document.createRange();
  r.selectNodeContents(svgText);
  const a = computeAnchor(source, document.getElementById("root")!, r)!;
  expect(a).toEqual({ start: 0, end: [...source].length });
});
