import { expect, test } from "vitest";
import { mapRenderedToSource } from "./map";

test("maps a selection across stripped markdown syntax", () => {
  const source = "This is **bold** text";
  const rendered = "This is bold text";
  const a = mapRenderedToSource(source, rendered, 8, 12); // "bold" in rendered
  expect(source.slice(a.start, a.end)).toBe("bold");
});

test("maps a plain (no-syntax) selection 1:1", () => {
  const source = "Plain sentence here";
  const a = mapRenderedToSource(source, source, 6, 14);
  expect(a).toEqual({ start: 6, end: 14 });
  expect(source.slice(a.start, a.end)).toBe("sentence");
});

test("maps a selection after an inline code span", () => {
  const source = "Run `go test` now";
  const rendered = "Run go test now";
  const a = mapRenderedToSource(source, rendered, 12, 15); // "now"
  expect(source.slice(a.start, a.end)).toBe("now");
});
