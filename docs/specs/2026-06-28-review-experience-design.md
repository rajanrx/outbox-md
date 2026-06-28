# Review Experience — Design Specification

| | |
|---|---|
| **Document** | Design Specification |
| **Project** | outbox-md |
| **Cycle** | v1-complete · slice 1 of N |
| **Status** | Draft — pending approval |
| **Date** | 2026-06-28 |
| **Builds on** | v1-core walking skeleton (merged) |

## 1. Goal

Make outbox-md **genuinely usable**: replace the raw `<textarea>` skeleton with a **rich, rendered Markdown reading experience** where a reviewer reads first, then comments in place, sees agent suggestions as a clear diff, and accepts/rejects — all on the existing safe-async backend.

## 2. Scope

**In this cycle:**
- Rich rendered Markdown reader (Mermaid, syntax-highlighted code, GFM tables, plugin seam).
- Select-to-comment with margin comment threads (reply, resolve).
- Suggestion review as a diff panel (accept / reject).
- Document sidebar with basic status.

**Explicitly deferred (additive later, no lock-in — do NOT build now):**
- **Governance** (approval, baseline, amendments, provenance) — its own next cycle.
- Inline tracked-changes rendered *within* the doc (diff panel is the cut).
- Config (`outbox.yaml`), reliability (leases/reaper/parked), dashboard, `DOCUMENT_LINK`, council.
- **Human direct-editing** — not built, but **not ruled out**: the Markdown-source-of-truth + version model means a future edit mode is an *additive surface*, not a rearchitecture.

## 3. Guiding constraints

- **Don't overengineer.** If something is complex and can be added later without lock-in, defer it.
- **Reuse the backend.** v1-core's anchors (`{start, end}` char offsets), versions, suggestions, and re-anchoring stay as-is. This cycle is **mostly frontend** plus a few small API endpoints.
- **Humans read, agents edit** — for now. Keep the edit door open via the version model.

## 4. Surface: rich rendered reader

Render Markdown to HTML read-only via a remark/rehype pipeline:
- **GFM** (tables, task lists, strikethrough) — `remark-gfm`.
- **Syntax highlighting** for code blocks — Shiki (or `rehype-highlight` if Shiki is heavy).
- **Mermaid** diagrams rendered to SVG — a render step on ` ```mermaid ` blocks.
- **Plugin seam** — block renderers are registered in one place so new block types (e.g. math) are a small add, not a refactor.

The reader is read-only. Editing is out of scope (§2).

## 5. Anchoring (R1 spike — built and validated FIRST)

Comments anchor to **source character offsets** (unchanged backend model). The new problem is mapping a selection on *rendered* output back to source offsets.

- **Prose** → the markdown AST carries source positions; a DOM text selection maps to source `{start, end}` offsets.
- **Rendered non-text blocks** (Mermaid, images, tables) → "comment on this block": the anchor is simply the **char range covering that block's source span**. **No schema change** — a block comment is an ordinary `{start, end}` anchor over the block's source extent; the frontend renders it as a block-level affordance. *(This is the key simplification that keeps the backend untouched.)*

**Spike acceptance:** render a doc → select prose → get correct source offsets; comment on a Mermaid block → anchor spans that block's source. If selection→offset cannot be made reliable, fall back to a **CodeMirror 6 source view** (syntax-highlighted source + decorations) — same backend, less pretty. **Nothing else is built until the spike passes.**

## 6. Review UX

- **Margin comments** (Google-Docs style): the anchored text is highlighted; a comment card sits in a right-hand margin aligned to its anchor. Click a highlight ↔ its card.
- **Create:** select text (or a block) → "Comment" → card appears, comment persists to the outbox.
- **Threads:** each comment shows its author and a thread; humans can **reply** freely and **resolve** (owner-only, per the model).
- **Outbox panel** remains as a filterable list (open / addressed / resolved), now cross-linked to the in-doc highlights.
- **Suggestion review:** when an agent attaches a suggestion, the comment card offers **Review** → a **diff panel** (current vs proposed, rendered or unified) with **Accept / Reject**. Accept runs the existing v1-core accept (new version, file rewrite, re-anchor).

## 7. Document sidebar

A left sidebar lists managed documents (from `/api/docs`) with a simple status (open-comment count). Click to open. This replaces "auto-open the first doc."

## 8. Backend deltas (small — most work is frontend)

- **Anchors unchanged** (`{start, end}`), including block comments (§5).
- **New API endpoints:**
  - `GET  /api/comments/{id}/thread` — thread messages.
  - `POST /api/comments/{id}/reply` — **human** reply (no claim token; humans reply freely).
  - `POST /api/comments/{id}/resolve` — owner resolves.
- Existing endpoints (`/api/docs`, `/api/docs/{id}`, comment create, suggestion, accept) stay; continue returning arrays (the v1-core null→`[]` fix).
- **MCP unchanged** this cycle.

## 9. Frontend structure

```
web/src/
  reader/          # rendered markdown + plugin registry (mermaid, code, …)
    Reader.tsx
    plugins.ts
  anchor/          # selection ↔ source-offset mapping (the spike lives here)
    map.ts
    map.test.ts
  comments/        # margin layer, comment card, thread, create
    Margin.tsx  Card.tsx  Thread.tsx
  suggestion/      # diff panel + accept/reject
    DiffPanel.tsx
  docs/            # sidebar
    Sidebar.tsx
  api.ts  App.tsx
```

Each unit is independently testable; the anchor map is pure and unit-tested (it's the risk).

## 10. Build order (incremental, checkpoint between)

1. **R1 spike** — `anchor/map.ts` + a minimal reader; prove prose→offset and block anchor (or fall back to CodeMirror).
2. **Rich reader** — mermaid, syntax highlighting, GFM, plugin seam.
3. **Margin comments + create + highlights.**
4. **Threads** — reply + resolve (+ the 3 small API endpoints).
5. **Suggestion diff panel** — accept / reject.
6. **Document sidebar.**

## 11. Risks

| # | Risk | Mitigation |
|---|---|---|
| R1 | Rendered selection → source offset mapping (esp. across mermaid/code/tables) | Spike first; CodeMirror source-view fallback. |
| R2 | Mermaid render cost / async on large docs | Render lazily/per-block; cache by block hash. |
| R3 | Margin alignment of comment cards to anchors as content reflows | Position by anchored element's DOM rect; recompute on layout change. |
| R4 | Shiki bundle size | Use a small theme/lang subset, or `rehype-highlight`. |

## 12. Non-goals restated

No governance, no config, no leases/dashboard, no inline tracked-changes, no human editing this cycle — all additive later.
