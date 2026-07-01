import { describe, expect, it } from "vitest";
import { alignedDiff, unifiedDiff, wordDiff, type AlignedRow } from "./diff";

// join a cell's segments back to plain text
const text = (segs: { text: string }[]) => segs.map((s) => s.text).join("");

describe("wordDiff", () => {
  it("segments a replaced line into eq/del/ins runs", () => {
    const segs = wordDiff("the quick brown fox", "the slow brown fox");
    // reconstructs the old string from eq+del, the new string from eq+ins
    expect(segs.filter((s) => s.op !== "ins").map((s) => s.text).join("")).toBe("the quick brown fox");
    expect(segs.filter((s) => s.op !== "del").map((s) => s.text).join("")).toBe("the slow brown fox");
    // there is a del run and an ins run, and the shared prefix/suffix stay eq
    expect(segs.some((s) => s.op === "del" && s.text.includes("quick"))).toBe(true);
    expect(segs.some((s) => s.op === "ins" && s.text.includes("slow"))).toBe(true);
    expect(segs.some((s) => s.op === "eq" && s.text.includes("brown fox"))).toBe(true);
  });

  it("marks an identical line as a single eq segment", () => {
    expect(wordDiff("same", "same")).toEqual([{ op: "eq", text: "same" }]);
  });
});

describe("alignedDiff row types", () => {
  it("keeps equal context rows around a change with matching line numbers", () => {
    const rows = alignedDiff("a\nb\nc", "a\nX\nc");
    const eqs = rows.filter((r) => r.type === "equal");
    expect(eqs).toHaveLength(2);
    // context lines keep their (equal) 1-based numbers on both sides
    expect(eqs[0].left?.num).toBe(1);
    expect(eqs[0].right?.num).toBe(1);
    expect(eqs[1].left?.num).toBe(3);
    expect(eqs[1].right?.num).toBe(3);
  });

  it("emits a pure insert row (left cell absent) with an eq segment only", () => {
    const rows = alignedDiff("a\nc", "a\nb\nc");
    const ins = rows.find((r) => r.type === "insert")!;
    expect(ins).toBeTruthy();
    expect(ins.left).toBeUndefined();
    expect(text(ins.right!.segs)).toBe("b");
    // pure insert must NOT carry ins/del word segments (light bg only)
    expect(ins.right!.segs.every((s) => s.op === "eq")).toBe(true);
  });

  it("emits a pure delete row (right cell absent) with an eq segment only", () => {
    const rows = alignedDiff("a\nb\nc", "a\nc");
    const del = rows.find((r) => r.type === "delete")!;
    expect(del).toBeTruthy();
    expect(del.right).toBeUndefined();
    expect(text(del.left!.segs)).toBe("b");
    expect(del.left!.segs.every((s) => s.op === "eq")).toBe(true);
  });

  it("pairs a changed line into a replace row with word-level segments", () => {
    const rows = alignedDiff("hello world", "hello there");
    const rep = rows.find((r) => r.type === "replace")!;
    expect(rep).toBeTruthy();
    // left carries eq+del, right carries eq+ins; neither carries the other's op
    expect(rep.left!.segs.some((s) => s.op === "del")).toBe(true);
    expect(rep.left!.segs.some((s) => s.op === "ins")).toBe(false);
    expect(rep.right!.segs.some((s) => s.op === "ins")).toBe(true);
    expect(rep.right!.segs.some((s) => s.op === "del")).toBe(false);
    expect(text(rep.left!.segs)).toBe("hello world");
    expect(text(rep.right!.segs)).toBe("hello there");
  });

  it("collapses a long unchanged run into a gap row", () => {
    const before = "change\n" + Array.from({ length: 20 }, (_, i) => `line${i}`).join("\n");
    const after = "CHANGED\n" + Array.from({ length: 20 }, (_, i) => `line${i}`).join("\n");
    const rows = alignedDiff(before, after);
    const gap = rows.find((r) => r.type === "gap") as AlignedRow;
    expect(gap).toBeTruthy();
    expect(gap.count).toBeGreaterThan(0);
    // gap spans both columns → no per-side cells
    expect(gap.left).toBeUndefined();
    expect(gap.right).toBeUndefined();
  });

  it("pairs a multi-line replace line-by-line", () => {
    const rows = alignedDiff("one\ntwo\nthree", "ONE\nTWO\nthree");
    const reps = rows.filter((r) => r.type === "replace");
    expect(reps).toHaveLength(2);
    expect(text(reps[0].left!.segs)).toBe("one");
    expect(text(reps[0].right!.segs)).toBe("ONE");
    expect(text(reps[1].left!.segs)).toBe("two");
    expect(text(reps[1].right!.segs)).toBe("TWO");
    // the trailing unchanged line stays a single equal row
    expect(rows.some((r) => r.type === "equal" && text(r.left!.segs) === "three")).toBe(true);
  });
});

describe("unifiedDiff flatten (inline view)", () => {
  it("groups a multi-line replace as all − rows then all + rows", () => {
    const rows = unifiedDiff("one\ntwo\nthree", "ONE\nTWO\nthree");
    const changeOps = rows.filter((r) => r.op === "del" || r.op === "ins").map((r) => r.op);
    // grouped GitHub-style, not interleaved del,ins,del,ins
    expect(changeOps).toEqual(["del", "del", "ins", "ins"]);
    // and inline rows carry word segments for highlighting
    const firstDel = rows.find((r) => r.op === "del")!;
    expect(firstDel.segs?.some((s) => s.op === "del")).toBe(true);
  });

  it("carries a gap row with a human label", () => {
    const before = "change\n" + Array.from({ length: 20 }, (_, i) => `line${i}`).join("\n");
    const after = "CHANGED\n" + Array.from({ length: 20 }, (_, i) => `line${i}`).join("\n");
    const rows = unifiedDiff(before, after);
    expect(rows.some((r) => r.op === "gap" && /unchanged lines/.test(r.text))).toBe(true);
  });
});
