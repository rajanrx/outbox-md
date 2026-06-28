// @vitest-environment jsdom
import { expect, test } from "vitest";
import { computeAnchor } from "./anchor";

test("prose selection → precise source range across syntax", () => {
  const source = "para before\n\nThis is **bold** text\n";
  const start = source.indexOf("This");
  const end = source.indexOf(" text");
  document.body.innerHTML =
    `<div id="root"><p data-pos-start="${start}" data-pos-end="${end}">This is bold text</p></div>`;
  const p = document.querySelector("p")!;
  const r = document.createRange();
  r.setStart(p.firstChild!, 8);
  r.setEnd(p.firstChild!, 12);
  const a = computeAnchor(source, document.getElementById("root")!, r)!;
  expect(source.slice(a.start, a.end)).toBe("bold");
});

test("mermaid block selection → whole-block anchor", () => {
  const source = "```mermaid\nflowchart LR\nA-->B\n```";
  document.body.innerHTML =
    `<div id="root"><div data-mermaid data-pos-start="0" data-pos-end="${source.length}"><svg></svg></div></div>`;
  const block = document.querySelector("[data-mermaid]")!;
  const r = document.createRange();
  r.selectNodeContents(block);
  const a = computeAnchor(source, document.getElementById("root")!, r)!;
  expect(a).toEqual({ start: 0, end: source.length });
});
