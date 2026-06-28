# Review Experience Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the skeleton `<textarea>` with a rich, rendered Markdown reading experience — Mermaid + syntax highlighting + GFM — where a reviewer selects text to comment in the margin, threads replies, and accepts/rejects agent suggestions via a diff panel.

**Architecture:** Mostly frontend on the unchanged v1-core backend. Markdown renders read-only via react-markdown with a custom rehype plugin that stamps **source character offsets** onto every element. A selection is mapped back to source `{start,end}` offsets (reusing the existing anchor model) by diffing the block's rendered text against its source text. Comments/suggestions use the existing API plus three small new endpoints (thread/reply/resolve).

**Tech Stack:** React + Vite + TS; `react-markdown`, `remark-gfm`, `rehype-highlight`, `mermaid`, `diff-match-patch`; Go backend (unchanged except 3 endpoints).

## Global Constraints

- **Backend anchors stay `{start, end}` rune/char offsets into source.** No schema change — block comments are a char-range over the block's source span.
- **Read-only reader.** No human editing this cycle (kept open architecturally, not built).
- **Don't overengineer.** Defer anything additive: governance, config, leases, dashboard, inline tracked-changes, cross-block selections.
- **Go:** `CGO_ENABLED=0`, Go ≥ 1.25, `modernc.org/sqlite`. **Frontend:** Vitest for pure logic; commit per task; DCO sign-off (`git commit -s`), author = repo owner, no Claude co-author.
- **The R1 anchor spike (Tasks 1–2) must pass before building Tasks 3+.** If selection→offset proves unreliable, fall back to a CodeMirror source view (out of plan; stop and re-plan).

## File structure

```
web/src/
  api.ts                      # MODIFY: add thread/reply/resolve + suggestion types
  App.tsx                     # MODIFY: compose sidebar + reader + margin
  anchor/
    map.ts                    # rendered→source offset mapping (spike core, pure)
    map.test.ts
    selection.ts              # DOM Selection → {blockEl, rStart, rEnd} (block-scoped)
  reader/
    Reader.tsx                # react-markdown render + source-position plugin
    rehypeSourcePos.ts        # rehype plugin: stamp data-pos-start/end
    mermaid.tsx               # MermaidBlock component
  comments/
    Margin.tsx                # right-margin comment cards aligned to anchors
    Card.tsx                  # one comment + thread + actions
    useComments.ts            # fetch/create/reply/resolve hooks
  suggestion/
    DiffPanel.tsx             # current vs proposed diff + accept/reject
  docs/
    Sidebar.tsx               # document list + open-count
internal/api/api.go           # MODIFY: GET thread, POST reply, POST resolve
internal/store/comments.go    # MODIFY: ListThread, (reply via AddThreadMessage), resolve guard
internal/service/service.go   # MODIFY: HumanReply, Resolve (owner-only)
```

---

## Task 1: Anchor mapping — rendered→source (the R1 spike core, TDD)

Pure function: given a block's source text and its rendered text, map a selection's rendered offsets to source offsets. This is the riskiest unit; it is fully tested.

**Files:**
- Create: `web/src/anchor/map.ts`, `web/src/anchor/map.test.ts`
- Modify: `web/package.json` (add `diff-match-patch`, `@types/diff-match-patch`)

**Interfaces:**
- Produces: `mapRenderedToSource(sourceText: string, renderedText: string, rStart: number, rEnd: number): { start: number; end: number }`

- [ ] **Step 1: Add the dep**

```bash
npm --prefix web install diff-match-patch
npm --prefix web install -D @types/diff-match-patch
```

- [ ] **Step 2: Write the failing test**

`web/src/anchor/map.test.ts`:
```ts
import { expect, test } from "vitest";
import { mapRenderedToSource } from "./map";

test("maps a selection across stripped markdown syntax", () => {
  const source = "This is **bold** text";
  const rendered = "This is bold text";
  // select "bold" in the rendered text → rendered [8,12)
  const a = mapRenderedToSource(source, rendered, 8, 12);
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
  // select "now" rendered [12,15)
  const a = mapRenderedToSource(source, rendered, 12, 15);
  expect(source.slice(a.start, a.end)).toBe("now");
});
```

- [ ] **Step 3: Run test to verify it fails**

Run: `npm --prefix web test -- map`
Expected: FAIL — cannot find `./map`.

- [ ] **Step 4: Implement**

`web/src/anchor/map.ts`:
```ts
import { diff_match_patch } from "diff-match-patch";

const dmp = new diff_match_patch();

// Map [rStart,rEnd) in renderedText to source offsets. Rendered text is the
// source with markdown syntax tokens removed, so we diff the two and translate
// each boundary. End is mapped via the last selected char to stay inside the
// source token (then +1) — diff_xIndex on an exclusive end can overshoot past
// trailing syntax.
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
```

- [ ] **Step 5: Run test to verify it passes**

Run: `npm --prefix web test -- map`
Expected: PASS (all 3). If an end boundary is off by the trailing token, adjust the end-mapping (this is the spike — tune until `source.slice` equals the selected words).

- [ ] **Step 6: Commit**

```bash
git add web/src/anchor/map.ts web/src/anchor/map.test.ts web/package.json web/package-lock.json
git commit -s -m "feat(reader): rendered→source offset mapping (anchor spike core)"
```

> **Spike gate:** if no end-mapping rule makes all three pass reliably, STOP — the rendered-anchor approach is at risk; fall back to a CodeMirror source view and re-plan.

## Task 2: Source-position rehype plugin + DOM selection → block offsets (spike, TDD where pure)

Stamp source offsets onto rendered elements, and turn a DOM selection into `{blockEl, rStart, rEnd}` scoped to one block.

**Files:**
- Create: `web/src/reader/rehypeSourcePos.ts`, `web/src/anchor/selection.ts`, `web/src/anchor/selection.test.ts`

**Interfaces:**
- Produces:
  - `rehypeSourcePos()` — a unified/rehype plugin: for every element with `node.position`, sets `properties['data-pos-start']` and `data-pos-end` (source offsets).
  - `blockTextOffsets(root: HTMLElement, range: Range): { blockEl: HTMLElement; rStart: number; rEnd: number } | null` — finds the nearest ancestor carrying `data-pos-start`, and the selection's offsets within that block's `textContent`. Returns null for cross-block or empty selections.

- [ ] **Step 1: Write the rehype plugin**

`web/src/reader/rehypeSourcePos.ts`:
```ts
import { visit } from "unist-util-visit";

// Stamp source character offsets from mdast/hast position data onto elements.
export function rehypeSourcePos() {
  return (tree: any) => {
    visit(tree, "element", (node: any) => {
      const pos = node.position;
      if (pos?.start?.offset != null && pos?.end?.offset != null) {
        node.properties = node.properties || {};
        node.properties["dataPosStart"] = String(pos.start.offset);
        node.properties["dataPosEnd"] = String(pos.end.offset);
      }
    });
  };
}
```
(Install dep: `npm --prefix web install unist-util-visit`.)

- [ ] **Step 2: Write the failing test for `blockTextOffsets`**

`web/src/anchor/selection.test.ts` (vitest with jsdom):
```ts
// @vitest-environment jsdom
import { expect, test } from "vitest";
import { blockTextOffsets } from "./selection";

test("finds the block and offsets within it", () => {
  document.body.innerHTML =
    `<div id="root"><p data-pos-start="0" data-pos-end="20">Hello brave world</p></div>`;
  const p = document.querySelector("p")!;
  const textNode = p.firstChild!;
  const range = document.createRange();
  range.setStart(textNode, 6);  // "brave"
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
```
Add jsdom: `npm --prefix web install -D jsdom`.

- [ ] **Step 3: Run test to verify it fails**

Run: `npm --prefix web test -- selection`
Expected: FAIL — cannot find `./selection`.

- [ ] **Step 4: Implement**

`web/src/anchor/selection.ts`:
```ts
function nearestBlock(node: Node | null): HTMLElement | null {
  let el = node instanceof HTMLElement ? node : node?.parentElement ?? null;
  while (el && !el.hasAttribute("data-pos-start")) el = el.parentElement;
  return el;
}

// Offset of `node`/`offset` within `block`'s textContent.
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
```

- [ ] **Step 5: Run test to verify it passes**

Run: `npm --prefix web test -- selection`
Expected: PASS (both).

- [ ] **Step 6: Commit**

```bash
git add web/src/reader/rehypeSourcePos.ts web/src/anchor/selection.ts web/src/anchor/selection.test.ts web/package.json web/package-lock.json
git commit -s -m "feat(reader): source-position plugin + block-scoped selection offsets"
```

---

## Task 3: Rich rendered reader (react-markdown + GFM + highlighting + Mermaid)

**Files:**
- Create: `web/src/reader/Reader.tsx`, `web/src/reader/mermaid.tsx`, `web/src/reader/reader.css`
- Modify: `web/package.json`

**Interfaces:**
- Produces: `Reader({ content, rootRef })` — renders Markdown read-only; every block element carries `data-pos-start/end`; Mermaid blocks render to SVG.

- [ ] **Step 1: Add deps**

```bash
npm --prefix web install react-markdown remark-gfm rehype-highlight mermaid
```

- [ ] **Step 2: Write the Mermaid block**

`web/src/reader/mermaid.tsx`:
```tsx
import { useEffect, useRef, useState } from "react";
import mermaid from "mermaid";

mermaid.initialize({ startOnLoad: false, theme: "dark", securityLevel: "strict" });

export function MermaidBlock({ chart }: { chart: string }) {
  const [svg, setSvg] = useState<string>("");
  const id = useRef("m" + Math.random().toString(36).slice(2)).current;
  useEffect(() => {
    let alive = true;
    mermaid.render(id, chart).then((r) => alive && setSvg(r.svg)).catch(() => alive && setSvg("<pre>mermaid error</pre>"));
    return () => { alive = false; };
  }, [chart, id]);
  return <div className="mermaid-block" data-mermaid dangerouslySetInnerHTML={{ __html: svg }} />;
}
```

- [ ] **Step 3: Write the Reader**

`web/src/reader/Reader.tsx`:
```tsx
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import rehypeHighlight from "rehype-highlight";
import "highlight.js/styles/github-dark.css";
import { rehypeSourcePos } from "./rehypeSourcePos";
import { MermaidBlock } from "./mermaid";
import "./reader.css";

export function Reader({ content, rootRef }: {
  content: string;
  rootRef: React.RefObject<HTMLDivElement | null>;
}) {
  return (
    <div ref={rootRef} className="reader markdown-body">
      <Markdown
        remarkPlugins={[remarkGfm]}
        rehypePlugins={[rehypeSourcePos, rehypeHighlight]}
        components={{
          code(props: any) {
            const cls: string = props.className || "";
            const text = String(props.children ?? "");
            if (cls.includes("language-mermaid")) return <MermaidBlock chart={text} />;
            return <code className={cls}>{props.children}</code>;
          },
        }}
      >
        {content}
      </Markdown>
    </div>
  );
}
```

- [ ] **Step 4: Minimal reader CSS**

`web/src/reader/reader.css`:
```css
.reader { max-width: 820px; margin: 0 auto; padding: 24px 32px; line-height: 1.6; color: #1f2328; font-family: -apple-system, system-ui, sans-serif; }
.reader pre { background: #0d1117; padding: 14px; border-radius: 8px; overflow: auto; }
.reader code { font-family: ui-monospace, monospace; }
.reader table { border-collapse: collapse; }
.reader th, .reader td { border: 1px solid #d0d7de; padding: 6px 12px; }
.reader ::selection { background: #fde68a; }
.mermaid-block { margin: 16px 0; }
::highlight(comment) { background: #fde68a; }
```

- [ ] **Step 5: Verify build**

Run: `npm --prefix web run build`
Expected: builds with no type errors.

- [ ] **Step 6: Commit**

```bash
git add web/src/reader/ web/package.json web/package-lock.json
git commit -s -m "feat(reader): rendered markdown reader with gfm, highlighting, mermaid"
```

## Task 4: Anchor assembly + create-comment (TDD on the pure assembly)

Combine source-position + selection + offset mapping into one `computeAnchor`, and wire "Comment" to the existing `POST /api/docs/{id}/comments`.

**Files:**
- Create: `web/src/anchor/anchor.ts`, `web/src/anchor/anchor.test.ts`
- Modify: `web/src/api.ts` (already has `postComment`; no change needed if signature matches)

**Interfaces:**
- Produces: `computeAnchor(source: string, root: HTMLElement, range: Range): { start: number; end: number } | null` — text-range for prose, whole-block for non-text/Mermaid blocks.

- [ ] **Step 1: Write the failing test**

`web/src/anchor/anchor.test.ts`:
```ts
// @vitest-environment jsdom
import { expect, test } from "vitest";
import { computeAnchor } from "./anchor";

test("prose selection → precise source range across syntax", () => {
  const source = "para before\n\nThis is **bold** text\n";
  const start = source.indexOf("This");
  const end = source.indexOf(" text"); // end of "**bold**"
  document.body.innerHTML =
    `<div id="root"><p data-pos-start="${start}" data-pos-end="${end}">This is bold text</p></div>`;
  const p = document.querySelector("p")!;
  const r = document.createRange();
  r.setStart(p.firstChild!, 8);  // "bold"
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npm --prefix web test -- anchor.test`
Expected: FAIL — cannot find `./anchor`.

- [ ] **Step 3: Implement**

`web/src/anchor/anchor.ts`:
```ts
import { blockTextOffsets } from "./selection";
import { mapRenderedToSource } from "./map";

export function computeAnchor(
  source: string,
  root: HTMLElement,
  range: Range,
): { start: number; end: number } | null {
  const sel = blockTextOffsets(root, range);
  if (!sel) return null;
  const ps = Number(sel.blockEl.getAttribute("data-pos-start"));
  const pe = Number(sel.blockEl.getAttribute("data-pos-end"));
  if (Number.isNaN(ps) || Number.isNaN(pe)) return null;
  const rendered = sel.blockEl.textContent ?? "";
  // Non-text / Mermaid / empty-text blocks: anchor the whole block.
  if (sel.blockEl.hasAttribute("data-mermaid") || !rendered.trim()) {
    return { start: ps, end: pe };
  }
  const m = mapRenderedToSource(source.slice(ps, pe), rendered, sel.rStart, sel.rEnd);
  return { start: ps + m.start, end: ps + m.end };
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npm --prefix web test -- anchor.test`
Expected: PASS (both).

- [ ] **Step 5: Commit**

```bash
git add web/src/anchor/anchor.ts web/src/anchor/anchor.test.ts
git commit -s -m "feat(reader): assemble source anchor from a rendered selection"
```

## Task 5: Margin comments — highlights + create + cards (manual verify)

Render existing comments as highlights over the rendered text, show cards in a right margin, and create a comment from the current selection.

**Files:**
- Create: `web/src/comments/useComments.ts`, `web/src/comments/Margin.tsx`, `web/src/comments/Card.tsx`, `web/src/comments/comments.css`

**Interfaces:**
- Consumes: `computeAnchor` (Task 4); `getDoc`, `postComment` (api.ts).
- Produces: `<Margin docId content rootRef comments onChange />`; `useComments(docId)` → `{ comments, refresh }`.

- [ ] **Step 1: Comments hook**

`web/src/comments/useComments.ts`:
```ts
import { useCallback, useEffect, useState } from "react";
import { getDoc, type Comment } from "../api";

export function useComments(docId: string) {
  const [comments, setComments] = useState<Comment[]>([]);
  const refresh = useCallback(async () => {
    if (!docId) return;
    const v = await getDoc(docId);
    setComments(v.comments ?? []);
  }, [docId]);
  useEffect(() => { refresh(); }, [refresh]);
  return { comments, refresh };
}
```

- [ ] **Step 2: Highlight + create logic in Margin**

`web/src/comments/Margin.tsx`:
```tsx
import { useEffect, useState } from "react";
import { computeAnchor } from "../anchor/anchor";
import { postComment, type Comment } from "../api";
import { Card } from "./Card";
import "./comments.css";

// Paint a highlight for each comment using the CSS Custom Highlight API,
// resolving anchor offsets to DOM ranges via the data-pos attributes.
function paintHighlights(root: HTMLElement, comments: Comment[]) {
  if (!("Highlight" in window) || !(CSS as any).highlights) return;
  const ranges: Range[] = [];
  for (const c of comments) {
    const block = [...root.querySelectorAll<HTMLElement>("[data-pos-start]")].find((el) => {
      const s = Number(el.getAttribute("data-pos-start"));
      const e = Number(el.getAttribute("data-pos-end"));
      return c.anchor.start >= s && c.anchor.end <= e;
    });
    if (!block) continue;
    const r = document.createRange();
    try { r.selectNodeContents(block); ranges.push(r); } catch { /* ignore */ }
  }
  (CSS as any).highlights.set("comment", new (window as any).Highlight(...ranges));
}

export function Margin({ docId, content, rootRef, comments, onChange }: {
  docId: string;
  content: string;
  rootRef: React.RefObject<HTMLDivElement | null>;
  comments: Comment[];
  onChange: () => void;
}) {
  const [pending, setPending] = useState<{ start: number; end: number } | null>(null);

  useEffect(() => {
    if (rootRef.current) paintHighlights(rootRef.current, comments);
  }, [comments, rootRef]);

  useEffect(() => {
    const onUp = () => {
      const selection = window.getSelection();
      if (!selection || selection.isCollapsed || !rootRef.current) return setPending(null);
      const range = selection.getRangeAt(0);
      setPending(computeAnchor(content, rootRef.current, range));
    };
    document.addEventListener("mouseup", onUp);
    return () => document.removeEventListener("mouseup", onUp);
  }, [content, rootRef]);

  return (
    <div className="margin">
      {pending && (
        <div className="margin-new">
          <button onClick={async () => { await postComment(docId, pending); setPending(null); window.getSelection()?.removeAllRanges(); onChange(); }}>
            Comment on selection
          </button>
        </div>
      )}
      {comments.map((c) => <Card key={c.id} comment={c} onChange={onChange} />)}
    </div>
  );
}
```

- [ ] **Step 3: Card (display only for now; thread added in Task 7)**

`web/src/comments/Card.tsx`:
```tsx
import type { Comment } from "../api";

export function Card({ comment }: { comment: Comment; onChange: () => void }) {
  return (
    <div className="card" data-comment={comment.id}>
      <div className="card-head">
        <span className={`who who-${comment.authorIdentity}`}>{comment.authorIdentity}</span>
        <span className={`status status-${comment.status}`}>{comment.status}</span>
      </div>
      <div className="card-anchor">chars {comment.anchor.start}–{comment.anchor.end}</div>
    </div>
  );
}
```

- [ ] **Step 4: CSS**

`web/src/comments/comments.css`:
```css
.margin { width: 320px; flex: none; padding: 24px 16px; border-left: 1px solid #d0d7de; overflow-y: auto; }
.card { border: 1px solid #d0d7de; border-radius: 8px; padding: 10px 12px; margin-bottom: 10px; font-size: 13px; }
.card-head { display: flex; justify-content: space-between; margin-bottom: 6px; }
.who { font-weight: 600; }
.who-human { color: #2f81f7; } .who-agent { color: #2dd4bf; }
.status { color: #6e7781; text-transform: capitalize; }
.margin-new { margin-bottom: 14px; }
```

- [ ] **Step 5: Verify build**

Run: `npm --prefix web run build`
Expected: builds clean.

- [ ] **Step 6: Commit**

```bash
git add web/src/comments/
git commit -s -m "feat(comments): margin cards, selection highlights, create-from-selection"
```

## Task 6: Backend — thread, reply, resolve endpoints (Go, TDD)

Humans need to read a thread, reply (no claim token), and resolve their own comment. These are small additions to the existing service/API.

**Files:**
- Modify: `internal/store/comments.go` (add `ListThread`), `internal/service/service.go` (add `HumanReply`, `Resolve`), `internal/api/api.go` (3 routes)
- Test: `internal/service/service_test.go`

**Interfaces:**
- Produces:
  - `func (s *Store) ListThread(commentID string) ([]domain.ThreadMessage, error)`
  - `func (s *Service) HumanReply(commentID, body string) (domain.ThreadMessage, error)`
  - `func (s *Service) Resolve(commentID, owner string) error` (owner-only)
  - API: `GET /api/comments/{id}/thread`, `POST /api/comments/{id}/reply` `{body}`, `POST /api/comments/{id}/resolve` `{owner}`

- [ ] **Step 1: Write the failing test**

Append to `internal/service/service_test.go`:
```go
func TestHumanReplyAndResolve(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := New(s, func(_, _ string) error { return nil })
	doc, _, _ := s.CreateDocument("spec.md", "Hello world", "human")
	c, _ := svc.PostComment(doc.ID, domain.Anchor{Start: 0, End: 5}, "human")

	if _, err := svc.HumanReply(c.ID, "what about X?"); err != nil {
		t.Fatal(err)
	}
	thread, _ := s.ListThread(c.ID)
	if len(thread) != 1 || thread[0].Body != "what about X?" {
		t.Fatalf("thread = %+v", thread)
	}
	// non-owner cannot resolve
	if err := svc.Resolve(c.ID, "someone-else"); err == nil {
		t.Fatal("expected non-owner resolve to fail")
	}
	if err := svc.Resolve(c.ID, "human"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetComment(c.ID)
	if got.Status != domain.CommentResolved {
		t.Fatalf("status = %s, want resolved", got.Status)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/service/ -run TestHumanReplyAndResolve`
Expected: FAIL — `svc.HumanReply` undefined.

- [ ] **Step 3: Implement store + service**

Append to `internal/store/comments.go`:
```go
func (s *Store) ListThread(commentID string) ([]domain.ThreadMessage, error) {
	rows, err := s.DB.Query(
		`SELECT id, comment_id, author_identity, body FROM thread_messages
		 WHERE comment_id=? ORDER BY created_at`, commentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.ThreadMessage{}
	for rows.Next() {
		var m domain.ThreadMessage
		if err := rows.Scan(&m.ID, &m.CommentID, &m.AuthorIdentity, &m.Body); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
```

Append to `internal/service/service.go`:
```go
func (s *Service) HumanReply(commentID, body string) (domain.ThreadMessage, error) {
	return s.store.AddThreadMessage(domain.ThreadMessage{
		CommentID: commentID, AuthorIdentity: "human", Body: body,
	})
}

func (s *Service) Resolve(commentID, owner string) error {
	c, err := s.store.GetComment(commentID)
	if err != nil {
		return err
	}
	if c.Owner != owner {
		return errors.New("only the comment owner may resolve it")
	}
	return s.store.UpdateCommentStatus(commentID, domain.CommentResolved, "")
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `CGO_ENABLED=0 go test ./internal/service/ -run TestHumanReplyAndResolve`
Expected: PASS.

- [ ] **Step 5: Add the API routes**

In `internal/api/api.go`, before `return mux`:
```go
	mux.HandleFunc("GET /api/comments/{id}/thread", func(w http.ResponseWriter, r *http.Request) {
		msgs, err := st.ListThread(r.PathValue("id"))
		writeJSON(w, msgs, err)
	})
	mux.HandleFunc("POST /api/comments/{id}/reply", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Body string `json:"body"` }
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		m, err := svc.HumanReply(r.PathValue("id"), in.Body)
		writeJSON(w, m, err)
	})
	mux.HandleFunc("POST /api/comments/{id}/resolve", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Owner string `json:"owner"` }
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := svc.Resolve(r.PathValue("id"), in.Owner); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]any{"ok": true}, nil)
	})
```

- [ ] **Step 6: Verify + commit**

Run: `CGO_ENABLED=0 go test ./... && go vet ./...`
Expected: all pass.
```bash
git add internal/
git commit -s -m "feat(api): thread, human reply, and owner-only resolve endpoints"
```

---

## Task 7: Threads in the comment card (reply + resolve)

**Files:**
- Modify: `web/src/api.ts` (thread/reply/resolve clients), `web/src/comments/Card.tsx`

**Interfaces:**
- Produces (api.ts): `getThread(commentId): Promise<ThreadMessage[]>`, `reply(commentId, body)`, `resolve(commentId, owner?)`; `type ThreadMessage = { id: string; authorIdentity: string; body: string }`.

- [ ] **Step 1: Extend the API client**

Append to `web/src/api.ts`:
```ts
export type ThreadMessage = { id: string; authorIdentity: string; body: string };

export async function getThread(commentId: string): Promise<ThreadMessage[]> {
  return (await fetch(`/api/comments/${commentId}/thread`)).json();
}
export async function reply(commentId: string, body: string): Promise<unknown> {
  return (await fetch(`/api/comments/${commentId}/reply`, { method: "POST", body: JSON.stringify({ body }) })).json();
}
export async function resolve(commentId: string, owner = "human"): Promise<unknown> {
  return (await fetch(`/api/comments/${commentId}/resolve`, { method: "POST", body: JSON.stringify({ owner }) })).json();
}
```

- [ ] **Step 2: Expand the Card**

Replace `web/src/comments/Card.tsx`:
```tsx
import { useEffect, useState } from "react";
import { getThread, reply, resolve, type Comment, type ThreadMessage } from "../api";

export function Card({ comment, onChange }: { comment: Comment; onChange: () => void }) {
  const [thread, setThread] = useState<ThreadMessage[]>([]);
  const [draft, setDraft] = useState("");
  const load = () => getThread(comment.id).then((t) => setThread(t ?? []));
  useEffect(() => { load(); /* eslint-disable-next-line */ }, [comment.id]);

  return (
    <div className="card" data-comment={comment.id}>
      <div className="card-head">
        <span className={`who who-${comment.authorIdentity}`}>{comment.authorIdentity}</span>
        <span className={`status status-${comment.status}`}>{comment.status}</span>
      </div>
      {thread.map((m) => (
        <div key={m.id} className="msg"><b className={`who-${m.authorIdentity}`}>{m.authorIdentity}:</b> {m.body}</div>
      ))}
      {comment.status !== "resolved" && (
        <div className="card-actions">
          <input value={draft} placeholder="Reply…" onChange={(e) => setDraft(e.target.value)} />
          <button disabled={!draft.trim()} onClick={async () => { await reply(comment.id, draft); setDraft(""); await load(); }}>Reply</button>
          <button onClick={async () => { await resolve(comment.id); onChange(); }}>Resolve</button>
        </div>
      )}
    </div>
  );
}
```

- [ ] **Step 3: Add CSS for messages**

Append to `web/src/comments/comments.css`:
```css
.msg { font-size: 13px; margin: 4px 0; }
.card-actions { display: flex; gap: 6px; margin-top: 8px; }
.card-actions input { flex: 1; min-width: 0; }
```

- [ ] **Step 4: Verify build + commit**

Run: `npm --prefix web run build`
Expected: builds clean.
```bash
git add web/src/api.ts web/src/comments/Card.tsx web/src/comments/comments.css
git commit -s -m "feat(comments): threaded replies + resolve in the card"
```

## Task 8: Suggestion diff panel (accept / reject)

**Files:**
- Create: `web/src/suggestion/DiffPanel.tsx`, `web/src/suggestion/diff.css`
- Modify: `web/src/api.ts` (`rejectSuggestion`), `internal/service/service.go` (`RejectSuggestion`), `internal/api/api.go` (reject route), `web/src/comments/Card.tsx` (open the panel)

**Interfaces:**
- Produces: `RejectSuggestion(commentID string) error` (service) → `POST /api/comments/{id}/reject`; `<DiffPanel commentId currentContent onDone />`.

- [ ] **Step 1: Backend reject (TDD)**

Append to `internal/service/service_test.go`:
```go
func TestRejectSuggestionReopens(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := New(s, func(_, _ string) error { return nil })
	doc, _, _ := s.CreateDocument("spec.md", "Hello world", "human")
	c, _ := svc.PostComment(doc.ID, domain.Anchor{Start: 0, End: 5}, "human")
	tok, _ := svc.Claim([]string{c.ID}, "agent")
	_, _ = svc.Propose(c.ID, tok, "Howdy world", "agent")
	if err := svc.RejectSuggestion(c.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetComment(c.ID)
	if got.Status != domain.CommentOpen {
		t.Fatalf("status = %s, want open", got.Status)
	}
}
```
Run to fail: `go test ./internal/service/ -run TestRejectSuggestionReopens` → undefined.

Append to `internal/service/service.go`:
```go
func (s *Service) RejectSuggestion(commentID string) error {
	sg, ok, err := s.store.GetSuggestionByComment(commentID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("no suggestion to reject")
	}
	_ = s.store.UpdateSuggestionState(sg.ID, domain.SuggestionRejected)
	return s.store.UpdateCommentStatus(commentID, domain.CommentOpen, "")
}
```
Run to pass: `CGO_ENABLED=0 go test ./internal/service/ -run TestRejectSuggestionReopens` → PASS.

Add route in `internal/api/api.go` (before `return mux`):
```go
	mux.HandleFunc("POST /api/comments/{id}/reject", func(w http.ResponseWriter, r *http.Request) {
		err := svc.RejectSuggestion(r.PathValue("id"))
		if err != nil { http.Error(w, err.Error(), http.StatusBadRequest); return }
		writeJSON(w, map[string]any{"ok": true}, nil)
	})
```

- [ ] **Step 2: API client**

Append to `web/src/api.ts`:
```ts
export async function rejectSuggestion(commentId: string): Promise<unknown> {
  return (await fetch(`/api/comments/${commentId}/reject`, { method: "POST" })).json();
}
```

- [ ] **Step 3: DiffPanel**

`web/src/suggestion/DiffPanel.tsx`:
```tsx
import { useEffect, useState } from "react";
import { diff_match_patch } from "diff-match-patch";
import { getSuggestion, accept, rejectSuggestion, type Suggestion } from "../api";
import "./diff.css";

const dmp = new diff_match_patch();

export function DiffPanel({ commentId, currentContent, onDone }: {
  commentId: string;
  currentContent: string;
  onDone: () => void;
}) {
  const [sg, setSg] = useState<Suggestion | null>(null);
  useEffect(() => { getSuggestion(commentId).then(setSg); }, [commentId]);
  if (!sg) return null;

  const diffs = dmp.diff_main(currentContent, sg.proposedContent);
  dmp.diff_cleanupSemantic(diffs);

  return (
    <div className="diff-overlay" onClick={onDone}>
      <div className="diff-panel" onClick={(e) => e.stopPropagation()}>
        <h4>Proposed change</h4>
        <pre className="diff">
          {diffs.map(([op, text], i) => (
            <span key={i} className={op === 1 ? "ins" : op === -1 ? "del" : "eq"}>{text}</span>
          ))}
        </pre>
        <div className="diff-actions">
          <button disabled={sg.state !== "proposed"} onClick={async () => { await accept(commentId); onDone(); }}>Accept</button>
          <button disabled={sg.state !== "proposed"} onClick={async () => { await rejectSuggestion(commentId); onDone(); }}>Reject</button>
          <button onClick={onDone}>Close</button>
        </div>
      </div>
    </div>
  );
}
```

- [ ] **Step 4: CSS**

`web/src/suggestion/diff.css`:
```css
.diff-overlay { position: fixed; inset: 0; background: rgba(0,0,0,.4); display: flex; align-items: center; justify-content: center; }
.diff-panel { background: #fff; border-radius: 10px; padding: 18px; width: min(820px, 92vw); max-height: 86vh; overflow: auto; }
.diff { white-space: pre-wrap; background: #f6f8fa; padding: 12px; border-radius: 8px; font-family: ui-monospace, monospace; font-size: 13px; }
.diff .ins { background: #aceebb; } .diff .del { background: #ffcecb; text-decoration: line-through; }
.diff-actions { display: flex; gap: 8px; margin-top: 12px; }
```

- [ ] **Step 5: Open the panel from the Card**

In `web/src/comments/Card.tsx`, add state `const [showDiff, setShowDiff] = useState(false);`, import `DiffPanel`, and when `comment.status === "addressed"` render a `<button onClick={() => setShowDiff(true)}>Review suggestion</button>` plus `{showDiff && <DiffPanel commentId={comment.id} currentContent={currentContent} onDone={() => { setShowDiff(false); onChange(); }} />}`. Pass `currentContent` into `Card` from `Margin` (thread the doc `content` prop down).

- [ ] **Step 6: Verify + commit**

Run: `CGO_ENABLED=0 go test ./... && npm --prefix web run build`
Expected: all pass; build clean.
```bash
git add internal/ web/src/suggestion/ web/src/api.ts web/src/comments/Card.tsx
git commit -s -m "feat(suggestion): diff panel with accept/reject + backend reject"
```

## Task 9: Document sidebar

**Files:**
- Create: `web/src/docs/Sidebar.tsx`, `web/src/docs/sidebar.css`

**Interfaces:**
- Produces: `<Sidebar docs activeId onSelect />` where `docs: {id, path}[]`.

- [ ] **Step 1: Sidebar**

`web/src/docs/Sidebar.tsx`:
```tsx
import "./sidebar.css";

export function Sidebar({ docs, activeId, onSelect }: {
  docs: { id: string; path: string }[];
  activeId: string;
  onSelect: (id: string) => void;
}) {
  return (
    <nav className="sidebar">
      <div className="sidebar-title">Documents</div>
      {docs.map((d) => (
        <button key={d.id} className={d.id === activeId ? "doc active" : "doc"} onClick={() => onSelect(d.id)}>
          {d.path}
        </button>
      ))}
    </nav>
  );
}
```

- [ ] **Step 2: CSS**

`web/src/docs/sidebar.css`:
```css
.sidebar { width: 240px; flex: none; border-right: 1px solid #d0d7de; padding: 16px 8px; overflow-y: auto; }
.sidebar-title { font-size: 12px; text-transform: uppercase; color: #6e7781; padding: 4px 8px 8px; }
.doc { display: block; width: 100%; text-align: left; padding: 6px 8px; border: 0; background: none; border-radius: 6px; cursor: pointer; font-size: 13px; }
.doc:hover { background: #f3f4f6; } .doc.active { background: #ddf4ff; font-weight: 600; }
```

- [ ] **Step 3: Verify + commit**

Run: `npm --prefix web run build`
```bash
git add web/src/docs/
git commit -s -m "feat(docs): document sidebar"
```

## Task 10: Wire it together + end-to-end verification

**Files:**
- Modify: `web/src/App.tsx`

- [ ] **Step 1: Compose the layout**

Replace `web/src/App.tsx`:
```tsx
import { useEffect, useRef, useState } from "react";
import { listDocs, getDoc, type DocView } from "./api";
import { Sidebar } from "./docs/Sidebar";
import { Reader } from "./reader/Reader";
import { Margin } from "./comments/Margin";
import { useComments } from "./comments/useComments";

export default function App() {
  const [docs, setDocs] = useState<{ id: string; path: string }[]>([]);
  const [docId, setDocId] = useState("");
  const [view, setView] = useState<DocView | null>(null);
  const rootRef = useRef<HTMLDivElement>(null);
  const { comments, refresh } = useComments(docId);

  useEffect(() => { listDocs().then((d) => { setDocs(d ?? []); if (d?.length) setDocId(d[0].id); }); }, []);
  useEffect(() => { if (docId) getDoc(docId).then(setView); }, [docId, comments]);

  if (!docs.length) return <div style={{ padding: 24 }}>No documents. Mount a folder of .md files.</div>;
  return (
    <div style={{ display: "flex", height: "100vh", fontFamily: "system-ui" }}>
      <Sidebar docs={docs} activeId={docId} onSelect={setDocId} />
      <div style={{ flex: 1, overflowY: "auto" }}>
        {view && <Reader content={view.content} rootRef={rootRef} />}
      </div>
      {view && (
        <Margin docId={docId} content={view.content} rootRef={rootRef} comments={comments} onChange={refresh} />
      )}
    </div>
  );
}
```

- [ ] **Step 2: Full build + tests**

Run: `CGO_ENABLED=0 go test ./... && npm --prefix web test && npm --prefix web run build`
Expected: all green.

- [ ] **Step 3: Live end-to-end (Docker)**

```bash
OUTBOX_DIR=docs/specs OUTBOX_DEV=1 docker compose up -d --build
# open http://localhost:8181
```
Verify by hand:
1. The design spec renders — headings, the metadata table, and the **Mermaid architecture diagram as an SVG**, code blocks syntax-highlighted.
2. Select a sentence → "Comment on selection" → a card appears in the right margin and the text stays highlighted.
3. Simulate an agent (dev mode): claim the comment and propose a change via `/api/dev/claim` + `/api/dev/propose` (see the v1-core README), then in the card click **Review suggestion** → the diff panel shows the change → **Accept** → the doc re-renders with the edit.
4. Reply in the card; Resolve it (owner) → it drops out of the active set.

- [ ] **Step 4: Commit**

```bash
git add web/src/App.tsx
git commit -s -m "feat(reader): compose sidebar + reader + margin into the review UI"
```

---

## Done criteria

- `go test ./...`, `go vet`, `npm test`, `npm run build` all pass.
- The R1 spike (Tasks 1–2, 4) proved rendered-selection → source-offset on prose and whole-block anchoring on Mermaid — all unit-tested.
- Live: a real spec renders richly (Mermaid + highlighting), you can comment in place, thread/resolve, and accept/reject a suggestion via the diff panel.
- Backend unchanged except 3 thread/reply/resolve endpoints + reject; anchors still `{start,end}`; governance untouched (next cycle).

