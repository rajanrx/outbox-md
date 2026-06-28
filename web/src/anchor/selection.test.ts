// @vitest-environment jsdom
import { expect, test } from "vitest";
import { blockTextOffsets } from "./selection";

test("finds the block and offsets within it", () => {
  document.body.innerHTML =
    `<div id="root"><p data-pos-start="0" data-pos-end="20">Hello brave world</p></div>`;
  const p = document.querySelector("p")!;
  const textNode = p.firstChild!;
  const range = document.createRange();
  range.setStart(textNode, 6);
  range.setEnd(textNode, 11);
  const root = document.getElementById("root")!;
  const got = blockTextOffsets(root, range);
  expect(got).not.toBeNull();
  expect(got!.blockEl).toBe(p);
  expect(got!.rStart).toBe(6);
  expect(got!.rEnd).toBe(11);
});

test("returns null when selection spans two blocks", () => {
  document.body.innerHTML =
    `<div id="root"><p data-pos-start="0" data-pos-end="5">aaa</p><p data-pos-start="6" data-pos-end="11">bbb</p></div>`;
  const ps = document.querySelectorAll("p");
  const range = document.createRange();
  range.setStart(ps[0].firstChild!, 0);
  range.setEnd(ps[1].firstChild!, 1);
  expect(blockTextOffsets(document.getElementById("root")!, range)).toBeNull();
});
