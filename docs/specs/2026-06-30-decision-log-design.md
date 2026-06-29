# outbox-md — Decision Log (Design)

| | |
|---|---|
| **Status** | Draft — pending approval |
| **Cycle** | v1.5 · decision log |
| **Date** | 2026-06-30 |
| **Branch** | `feat/decision-log` |

## 1. Summary

A read-only, per-document **timeline** of the meaningful events on a document — feedback raised, changes proposed, edits applied, and approvals — so anyone can see *what changed, who did it, and why* at a glance. It is a pure **read-view** derived on the fly from data already captured (`comments`, `suggestions`, `versions`, `approvals`); it adds no new writes and nothing to keep in sync. Advances auditability (G4) and the v1.5 "auto changelog / decision log."

## 2. Scope

**In scope (this cycle):**
- Per-document decision log, derived live from existing event rows.
- Event kinds: document created, comment raised, change proposed, edit applied, approved / re-approved.
- `GET /api/docs/{id}/log` endpoint.
- A "History" timeline view in the UI.

**Out of scope (later cycles):**
- MCP / agent-query access to the log (the deeper "agents trace back the corpus" north-star — its own slice).
- Cross-document / corpus-wide log.
- Thread messages and un-timestamped state-flips (resolve, accept/reject) as their own entries — the "why" surfaces through the comment's anchored text and the approval note, so raw discussion stays out (it's a decision log, not a chat log).
- Materialization / a dedicated events table (derived live is sufficient at single-document scale).

## 3. Data sources

Every source row already has a `created_at` (`datetime('now')`, format `YYYY-MM-DD HH:MM:SS`, lexicographically sortable):

| Source table | Becomes | Actor | Extra |
|---|---|---|---|
| `versions` (ordinal 1) | `created` | `created_by` | version ordinal |
| `versions` (ordinal > 1) | `edit` | `created_by` | version ordinal |
| `comments` | `comment` | `author_identity` | anchored-text excerpt |
| `suggestions` | `proposal` | `created_by` | — |
| `approvals` | `approval` | `approved_by` | version ordinal, note, re-approval flag |

`suggestions` link to a document via `comment_id → comments.doc_id`. `approvals.version_id → versions.ordinal`. A comment's excerpt is its anchor `[start,end]` (rune offsets) sliced from the content of its `against_version_id`.

## 4. Backend

### 4.1 Domain
```go
type LogEntry struct {
	Time       string `json:"time"`        // created_at
	Kind       string `json:"kind"`        // "created" | "comment" | "proposal" | "edit" | "approval"
	Actor      string `json:"actor"`       // identity
	Detail     string `json:"detail"`      // comment excerpt OR approval note OR ""
	Version    int    `json:"version"`     // version ordinal for created/edit/approval; 0 otherwise
	ReApproval bool   `json:"reApproval"`  // true for an approval after the first (amendment sign-off)
}
```
The backend returns **structured** entries; the frontend composes the human phrasing and icons. This keeps the store a data provider and presentation in one place.

### 4.2 Store
`func (s *Store) ListDecisionLog(docID string) ([]domain.LogEntry, error)`:
1. Load the doc's `versions` (id→ordinal, content, created_by, created_at), `comments`, `suggestions` (joined via the doc's comment ids), and `approvals`.
2. Map each row to a `LogEntry`:
   - version ordinal 1 → `created`; ordinal > 1 → `edit`; `Version = ordinal`.
   - comment → `comment`; `Detail` = the anchor sliced from `versions[against_version_id].content` via `[]rune` (matching the existing rune-offset anchor convention); empty/clipped safely if the version or range is missing.
   - suggestion → `proposal`.
   - approval → `approval`; `Version` = ordinal of `version_id`; `Detail` = `note`; `ReApproval` = not the first approval (by time) for the doc.
3. Sort ascending by `(Time, kind-priority)` — kind-priority is a fixed tiebreak (`created < comment < proposal < edit < approval`) so events sharing a one-second `created_at` order deterministically.

No new tables, columns, or writes.

### 4.3 API
`GET /api/docs/{id}/log` → `[]LogEntry` (JSON; `[]` when empty). Read-only; no auth (consistent with the rest of the local-first API).

## 5. Frontend

- `web/src/api.ts`: a `LogEntry` type mirroring the backend, and `getLog(id): Promise<LogEntry[]>`.
- A **History** toggle button in the top bar (beside the lifecycle controls) opens a `DecisionLog` timeline panel (same overlay/panel pattern as the baseline-diff view).
- `DecisionLog` renders entries **oldest-first** (chronological), each row: a **kind icon**, the **actor**, a **phrased summary** composed in the frontend from `Kind`/`Version`/`ReApproval`, a **timestamp**, and the **detail** (excerpt in quotes for comments, note in quotes for approvals).
  - Phrasing: `created` → "created the document (v1)"; `comment` → "commented on “{detail}”"; `proposal` → "proposed a change"; `edit` → "applied an edit → v{version}"; `approval` → `reApproval ? "re-approved v{version}" : "approved v{version}"`, with the note appended when present.
- Styling reuses the existing design tokens (`--accent`, `--chrome-*`, `--ink`, `--agent`); identity colouring matches comments (human = accent, agent = teal).

## 6. Testing

- **Store** (`internal/store`): run a scripted lifecycle (create doc → comment → propose → accept⇒version → approve → post-approval comment → propose → accept⇒amending version → re-approve) and assert `ListDecisionLog` returns the entries in time order with the right `Kind`, `Actor`, `Version`, `ReApproval`, and a non-empty comment `Detail` excerpt; assert the same-second tiebreak ordering is deterministic.
- **API** (`internal/api`): `GET /api/docs/{id}/log` returns the entries as JSON; empty doc returns `[]`.
- **Frontend:** `tsc --noEmit` + `npm run build`; live check that History opens and renders the timeline against a doc taken through the lifecycle.

## 7. Decisions

- **Order:** the timeline renders **oldest-first** (chronological narrative of how the doc evolved); the store returns ascending and the UI can reverse if newest-first reads better — pick oldest-first for v1.
- **Excerpt length:** comment excerpts are clipped to a reasonable length (e.g. 80 chars) with an ellipsis, computed in the store from the anchored text.
- **No `created` noise:** only one `created` entry (the ordinal-1 version); the on-disk import is that single event.
- **Errors:** a missing `against_version` for a comment yields an empty excerpt rather than failing the whole log.
