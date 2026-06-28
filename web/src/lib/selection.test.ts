import { expect, test } from "vitest";
import { selectionToAnchor } from "./selection";

test("ascii selection maps 1:1", () => {
  expect(selectionToAnchor("Hello world", 6, 11)).toEqual({ start: 6, end: 11 });
});

test("astral chars count as one rune", () => {
  // "😀x": the emoji is 2 UTF-16 units; selecting "x" is JS [2,3) → runes [1,2)
  expect(selectionToAnchor("😀x", 2, 3)).toEqual({ start: 1, end: 2 });
});
