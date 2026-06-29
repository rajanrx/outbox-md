# outbox-md — Governance Seam & Amendment Cycle (Design)

| | |
|---|---|
| **Status** | Draft — pending approval |
| **Cycle** | v1.5 · governance foundation |
| **Date** | 2026-06-28 |
| **Branch** | `feat/governance-seam` |
| **Supersedes intent of** | §12 of `2026-06-27-outbox-md-design.md` (this cycle implements that seam) |

## 1. Summary

Give documents a governed lifecycle and an approved baseline. A human **approves** a document, pinning a specific version as the **baseline** — the on-disk `.md` *is* that baseline. After approval, comments are still allowed but are flagged `post_approval`; accepting their suggestions does **not** move the baseline. Instead new versions accumulate ahead of the baseline (`amending`), the on-disk file stays at the baseline, and a human **re-approves** to advance the baseline and write it to disk. The result is a complete, auditable record of what changed after sign-off.

This is the "safe async iteration after sign-off" story: an approved document is never silently changed; every post-approval edit is a tracked amendment requiring re-approval.

## 2. Scope

**In scope (this cycle):**
- Document lifecycle: `draft → approved → amending → approved`.
- Approve / Re-approve operations (human-only, HTTP API).
- `APPROVAL` append-only history.
- `post_approval` flag auto-set on comments created against an approved/amending doc.
- Accept-suggestion behaviour branches on lifecycle (draft writes disk; approved/amending accumulates a pending amendment, disk unchanged).
- UI: lifecycle badge, Approve / Re-approve, and a diff-against-baseline view while amending.
- MCP `read_doc` exposes `status` and `approvedVersionId`.

**Out of scope (later cycles):**
- `in_review` state (deliberately dropped — see §3).
- Approver roles (commenter vs approver), council, semantic blame, auto changelog, document links.
- Batch re-approval workflows beyond a single Re-approve action.
- Authentication (the app stays local-first / single-user / unauthenticated per `SECURITY.md`).

## 3. Decisions

- **No `in_review` state.** The master spec listed `draft → in_review → approved`. In a single-user local tool the manual "ready for review" gate is ceremony with no second party; this cycle uses `draft → approved` directly. The lifecycle remains extensible if a review gate is wanted later.
- **Approval is human-only and not an MCP operation.** Approve/Re-approve are HTTP endpoints with a **server-set** identity (`LocalHuman = "human"`, the same pattern as `Resolve`). No identity is taken from the request body (which would be spoofable); no authentication is added.
- **Approve is allowed with open comments.** Approval pins the current working version as the baseline. Unresolved comments remain open and, if later addressed, become amendments. We do not block approval on outstanding comments.
- **Disk invariant depends on lifecycle.** For `draft`, the on-disk `.md` equals the current version (today's behaviour). For `approved`/`amending`, the on-disk `.md` equals the **approved baseline**, not the working head. Re-approve is the only thing that advances the on-disk file once a baseline exists.

## 4. Domain & data model

**`Document`** gains:
- `status` — `draft | approved | amending` (default `draft`).
- `approvedVersionId` — the pinned baseline version id; empty until first approval.
- (`currentVersionId` already exists and remains the working head.)

**`Approval`** (new, append-only):
```
{ id, docId, versionId, approvedBy, note, createdAt }
```
A new row is written on every approve and re-approve. The latest row for a doc corresponds to its current `approvedVersionId`.

**`Comment`** gains:
- `postApproval bool` — set true at creation time when the doc's status is `approved` or `amending`.

SQLite migration: add the three columns and the `approvals` table. Existing rows default to `status='draft'`, `approved_version_id=''`, `post_approval=0`, preserving current behaviour.

## 5. Operations

### 5.1 Approve (`draft → approved`)
`POST /api/docs/{id}/approve` (body: optional `{note}`).
- Pins `approvedVersionId = currentVersionId`, sets `status = approved`.
- Writes an `APPROVAL` record (`versionId = currentVersionId`, `approvedBy = LocalHuman`, `note`).
- On-disk file already equals the current version, so no rewrite is needed; the current version simply *becomes* the baseline.

### 5.2 Accept on an approved/amending doc (amendment accumulation)
`Accept(commentID)` (existing) branches on `doc.status`:
- **`draft`** — unchanged: CAS-add a version **and** write the file in one transaction (today's `AddVersionTx` path).
- **`approved` / `amending`** — CAS-add a new version advancing `currentVersionId`, set `status = amending`, but **do not write the file** (disk stays at the baseline). The comment/suggestion lifecycle and anchor rebasing are unchanged. The existing stale-version CAS guard still serializes concurrent accepts.

### 5.3 Re-approve (`amending → approved`)
`POST /api/docs/{id}/reapprove` (body: optional `{note}`).
- Advances `approvedVersionId = currentVersionId`, sets `status = approved`.
- **Writes the current version's content to the on-disk `.md`** (the baseline now equals the working head).
- Writes a new `APPROVAL` record.
- No-op guard: re-approving when `currentVersionId == approvedVersionId` is rejected ("nothing to re-approve").

### 5.4 PostComment
Sets `postApproval = (doc.status == approved || doc.status == amending)` at creation. Otherwise unchanged.

## 6. UI

- **Lifecycle badge** in the top bar: `Draft` / `Approved` / `Amending`, beside the breadcrumb.
- **Approve button** (top bar) when `draft`; **Re-approve button** when `amending`. Both call the respective endpoint then refresh.
- **Amendment view**: while `amending`, a control opens a **diff-against-baseline** panel (approved baseline content vs current working head), reusing the existing `DiffPanel` rendering. This shows "what changed after sign-off" before re-approving.
- Comments carry a small `post-approval` marker so a reviewer can see which feedback arrived after approval.

## 7. MCP

`read_doc` response includes `status` and `approvedVersionId` (and, when approved, the baseline content is what the on-disk file holds). Agents thus know that commenting on an approved doc yields amendments. No agent approve/re-approve operation exists — approval stays human.

## 8. Error handling & invariants

- **One baseline.** Approve/Re-approve set `approvedVersionId` atomically; the `APPROVAL` history is append-only and never rewritten.
- **Disk matches the governed state.** `draft` → disk == current version; `approved`/`amending` → disk == approved baseline. Accept on an approved/amending doc never writes disk; only Re-approve does.
- **Concurrency.** Accept keeps its compare-and-swap on the current version, so concurrent accepts cannot both advance past the same version, in any lifecycle state. Re-approve reads the current head and advances the baseline to it; a concurrent accept that lands first simply means re-approve pins a later head (acceptable — the human is approving "everything up to now").
- **Guards (definite).** `approve` is valid only from `draft`; called on an `approved`/`amending` doc it is rejected ("already approved — use re-approve"). `reapprove` is valid only from `amending` with `currentVersionId != approvedVersionId`; otherwise rejected ("nothing to re-approve"). This keeps each transition single-purpose and the `APPROVAL` history meaningful.

## 9. Testing

Go store/service tests in the existing table + `-race` style:
- Approve pins exactly one baseline and writes one `APPROVAL`; disk unchanged.
- Accept on `approved` advances the working head, sets `amending`, and **leaves the on-disk file at the baseline**.
- Re-approve advances the baseline, writes the head to disk, appends an `APPROVAL`.
- Re-approve with nothing pending is rejected.
- Concurrent accepts on an approved doc: exactly one wins per version (CAS), disk stays at baseline throughout.
- `PostComment` sets `postApproval` correctly per lifecycle state.
- MCP `read_doc` returns `status` + `approvedVersionId`.

Frontend: lifecycle badge reflects status; Approve/Re-approve call the endpoints and refresh; diff-against-baseline renders while amending.
