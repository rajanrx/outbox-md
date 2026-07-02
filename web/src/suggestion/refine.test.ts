import { describe, expect, it } from "vitest";
import {
  canRefine,
  clampSnippet,
  formatRefineMessage,
  groupDrafts,
  lineKey,
  type LineDraft,
} from "./refine";

const draft = (over: Partial<LineDraft>): LineDraft => ({
  id: Math.random().toString(36),
  side: "new",
  lineNo: 1,
  snippet: "some line",
  body: "fix this",
  ...over,
});

describe("lineKey", () => {
  it("is a stable side:lineNo string", () => {
    expect(lineKey("new", 12)).toBe("new:12");
    expect(lineKey("old", 5)).toBe("old:5");
  });
});

describe("canRefine", () => {
  it("is false with no drafts", () => {
    expect(canRefine([])).toBe(false);
  });
  it("is true with at least one non-empty draft", () => {
    expect(canRefine([draft({ body: "note" })])).toBe(true);
  });
  it("ignores whitespace-only drafts", () => {
    expect(canRefine([draft({ body: "   " })])).toBe(false);
  });
});

describe("groupDrafts", () => {
  it("buckets drafts by line key preserving order", () => {
    const a = draft({ id: "a", side: "new", lineNo: 3, body: "one" });
    const b = draft({ id: "b", side: "new", lineNo: 3, body: "two" });
    const c = draft({ id: "c", side: "old", lineNo: 3, body: "three" });
    const m = groupDrafts([a, b, c]);
    expect(m.get("new:3")?.map((d) => d.id)).toEqual(["a", "b"]);
    expect(m.get("old:3")?.map((d) => d.id)).toEqual(["c"]);
  });
});

describe("clampSnippet", () => {
  it("collapses whitespace and trims", () => {
    expect(clampSnippet("  the   quick\tbrown\nfox  ")).toBe("the quick brown fox");
  });
  it("truncates long snippets with an ellipsis", () => {
    const long = "x".repeat(100);
    const out = clampSnippet(long, 10);
    expect(out.length).toBe(10);
    expect(out.endsWith("…")).toBe(true);
  });
});

describe("formatRefineMessage", () => {
  it("returns empty string for no drafts (button stays disabled)", () => {
    expect(formatRefineMessage([])).toBe("");
  });

  it("drops drafts with empty bodies", () => {
    expect(formatRefineMessage([draft({ body: "  " })])).toBe("");
  });

  it("formats a single draft with a header, quoted snippet and trimmed body", () => {
    const msg = formatRefineMessage([
      draft({ side: "new", lineNo: 12, snippet: "the quick brown fox", body: "  too informal  " }),
    ]);
    expect(msg).toBe(
      "Refine — inline feedback on your suggestion:\n" +
        "- line 12 «the quick brown fox»: too informal",
    );
  });

  it("orders items by line number and annotates removed (old-side) lines", () => {
    const msg = formatRefineMessage([
      draft({ side: "new", lineNo: 18, snippet: "keep this", body: "drop this sentence" }),
      draft({ side: "old", lineNo: 5, snippet: "old text", body: "restore" }),
      draft({ side: "new", lineNo: 12, snippet: "brown fox", body: "use plain language" }),
    ]);
    expect(msg).toBe(
      [
        "Refine — inline feedback on your suggestion:",
        "- line 5 (removed) «old text»: restore",
        "- line 12 «brown fox»: use plain language",
        "- line 18 «keep this»: drop this sentence",
      ].join("\n"),
    );
  });

  it("includes the snippet clamped for long lines", () => {
    const long = "word ".repeat(40);
    const msg = formatRefineMessage([draft({ lineNo: 3, snippet: long, body: "shorten" })]);
    const snippetPart = msg.split("«")[1].split("»")[0];
    expect(snippetPart.length).toBeLessThanOrEqual(48);
    expect(snippetPart.endsWith("…")).toBe(true);
  });
});
