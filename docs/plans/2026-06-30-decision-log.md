# Decision Log Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A read-only per-document decision-log timeline — who did what, when, and why — derived live from existing event rows.

**Architecture:** A single store function merges `versions`, `comments`, `suggestions`, and `approvals` (each already timestamped) into a time-ordered `[]LogEntry`; an HTTP endpoint serves it; a "History" panel renders it. No new tables, columns, or writes.

**Tech Stack:** Go 1.25 (`database/sql`, `modernc.org/sqlite`), React 19 + TypeScript + Vite.

**Spec:** `docs/specs/2026-06-30-decision-log-design.md`

## Global Constraints

- Commit identity is `rajan <rajanrauniyar@gmail.com>`. NEVER add a `Co-Authored-By: Claude` trailer. Use `git -c user.name='rajan' -c user.email='rajanrauniyar@gmail.com' commit -m "..."`.
- Read-only feature: no new tables/columns, no writes, no new dependencies.
- `LogEntry` shape is a contract: `{ time, kind, actor, detail, version, reApproval }`. Kinds: `created | comment | proposal | edit | approval`.
- Entries sort ascending by `(time, kind-priority)` where kind-priority is `created<comment<proposal<edit<approval` (deterministic tiebreak for same-second events).
- Comment excerpts clip to 80 runes with an `…`; a missing source version yields an empty excerpt, never an error.
- Local-first / unauthenticated — the `/log` endpoint adds no auth, consistent with the rest of the API.
- Go tests run in the dev backend container: `docker compose -f docker-compose.dev.yml exec -T backend go test ./...` (single pkg: `... go test ./internal/store/ -run TestName -v`). Frontend: `cd web && npx tsc --noEmit` then `npm run build`.

---

### Task 1: Domain `LogEntry` + store `ListDecisionLog`

**Files:**
- Modify: `internal/domain/domain.go` (add `LogEntry`)
- Create: `internal/store/log.go`
- Test: `internal/store/log_test.go`

**Interfaces:**
- Consumes: existing `(*Store).CreateDocument`, `(*Store).CreateComment`, `(*Store).CreateSuggestion`, `(*Store).AddVersion`, `(*Store).CreateApproval`, and the `versions`/`comments`/`suggestions`/`approvals` tables.
- Produces: `domain.LogEntry{Time, Kind, Actor, Detail string; Version int; ReApproval bool}` and `(*Store).ListDecisionLog(docID string) ([]domain.LogEntry, error)` — ascending by `(time, kind-priority)`.

- [ ] **Step 1: Write the failing test** — create `internal/store/log_test.go` (package `store`; opens with `Open(":memory:")` like the other store tests):

```go
package store

import (
	"testing"

	"github.com/rajanrx/outbox-md/internal/domain"
)

func TestListDecisionLog(t *testing.T) {
	s, _ := Open(":memory:")
	defer s.Close()

	doc, v1, _ := s.CreateDocument("spec.md", "hello world base", "human") // v1 = created
	if _, err := s.CreateComment(domain.Comment{
		DocID: doc.ID, AgainstVersionID: v1.ID, Anchor: domain.Anchor{Start: 6, End: 11}, // "world"
		AuthorIdentity: "human", Owner: "human", Status: domain.CommentOpen,
	}); err != nil {
		t.Fatal(err)
	}
	c, _ := s.GetComment(firstCommentID(t, s, doc.ID))
	if _, err := s.CreateSuggestion(domain.Suggestion{
		CommentID: c.ID, AgainstVersionID: v1.ID, ProposedContent: "hello world v2",
		State: domain.SuggestionProposed, CreatedBy: "agent",
	}); err != nil {
		t.Fatal(err)
	}
	v2, _ := s.AddVersion(doc.ID, "hello world v2", "agent") // edit
	if _, err := s.CreateApproval(domain.Approval{DocID: doc.ID, VersionID: v2.ID, ApprovedBy: "human", Note: "ok"}); err != nil {
		t.Fatal(err)
	}
	v3, _ := s.AddVersion(doc.ID, "hello world v3", "agent") // amend edit
	if _, err := s.CreateApproval(domain.Approval{DocID: doc.ID, VersionID: v3.ID, ApprovedBy: "human", Note: "amended"}); err != nil {
		t.Fatal(err)
	}

	log, err := s.ListDecisionLog(doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Same-second events sort by kind-priority: created < comment < proposal < edit < approval.
	kinds := []string{}
	for _, e := range log {
		kinds = append(kinds, e.Kind)
	}
	want := []string{"created", "comment", "proposal", "edit", "edit", "approval", "approval"}
	if len(kinds) != len(want) {
		t.Fatalf("kinds = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("kinds = %v, want %v", kinds, want)
		}
	}
	// Field checks.
	if log[0].Version != 1 || log[0].Actor != "human" {
		t.Errorf("created entry = %+v", log[0])
	}
	if log[1].Detail != "world" {
		t.Errorf("comment excerpt = %q, want world", log[1].Detail)
	}
	if log[5].Kind != "approval" || log[5].Version != 2 || log[5].Detail != "ok" || log[5].ReApproval {
		t.Errorf("first approval = %+v, want v2 note=ok reApproval=false", log[5])
	}
	if log[6].Version != 3 || !log[6].ReApproval {
		t.Errorf("second approval = %+v, want v3 reApproval=true", log[6])
	}
}

// firstCommentID returns the only comment's id for a doc (test helper).
func firstCommentID(t *testing.T, s *Store, docID string) string {
	t.Helper()
	cs, err := s.ListComments(docID)
	if err != nil || len(cs) == 0 {
		t.Fatalf("no comments: %v", err)
	}
	return cs[0].ID
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `docker compose -f docker-compose.dev.yml exec -T backend go test ./internal/store/ -run TestListDecisionLog -v`
Expected: FAIL (compile error: `domain.LogEntry` / `ListDecisionLog` undefined).

- [ ] **Step 3: Add the domain type** — append to `internal/domain/domain.go`:

```go
type LogEntry struct {
	Time       string `json:"time"`
	Kind       string `json:"kind"`       // created | comment | proposal | edit | approval
	Actor      string `json:"actor"`
	Detail     string `json:"detail"`     // comment excerpt OR approval note OR ""
	Version    int    `json:"version"`    // version ordinal for created/edit/approval; 0 otherwise
	ReApproval bool   `json:"reApproval"` // approval after the first (amendment sign-off)
}
```

- [ ] **Step 4: Implement the store function** — create `internal/store/log.go`:

```go
package store

import (
	"sort"

	"github.com/rajanrx/outbox-md/internal/domain"
)

// kindOrder is a deterministic tiebreak for events that share a one-second
// created_at timestamp.
var kindOrder = map[string]int{"created": 0, "comment": 1, "proposal": 2, "edit": 3, "approval": 4}

const excerptMax = 80

func excerpt(content string, start, end int) string {
	r := []rune(content)
	if start < 0 {
		start = 0
	}
	if end > len(r) {
		end = len(r)
	}
	if start >= end {
		return ""
	}
	out := []rune(string(r[start:end]))
	if len(out) > excerptMax {
		return string(out[:excerptMax]) + "…"
	}
	return string(out)
}

// ListDecisionLog returns the document's decision timeline, ascending by
// (time, kind-priority), derived live from versions, comments, suggestions, and
// approvals. No writes.
func (s *Store) ListDecisionLog(docID string) ([]domain.LogEntry, error) {
	out := []domain.LogEntry{}

	// versions → created (ordinal 1) / edit (ordinal > 1); keep content + ordinal
	// maps for comment excerpts and approval ordinals.
	verContent := map[string]string{}
	verOrdinal := map[string]int{}
	vrows, err := s.DB.Query(`SELECT id, ordinal, content, created_by, created_at FROM versions WHERE doc_id=? ORDER BY ordinal`, docID)
	if err != nil {
		return nil, err
	}
	for vrows.Next() {
		var id, content, by, at string
		var ord int
		if err := vrows.Scan(&id, &ord, &content, &by, &at); err != nil {
			vrows.Close()
			return nil, err
		}
		verContent[id] = content
		verOrdinal[id] = ord
		kind := "edit"
		if ord == 1 {
			kind = "created"
		}
		out = append(out, domain.LogEntry{Time: at, Kind: kind, Actor: by, Version: ord})
	}
	vrows.Close()
	if err := vrows.Err(); err != nil {
		return nil, err
	}

	// comments → comment; excerpt sliced from the against-version content.
	crows, err := s.DB.Query(`SELECT against_version_id, anchor_start, anchor_end, author_identity, created_at FROM comments WHERE doc_id=?`, docID)
	if err != nil {
		return nil, err
	}
	for crows.Next() {
		var avid, by, at string
		var start, end int
		if err := crows.Scan(&avid, &start, &end, &by, &at); err != nil {
			crows.Close()
			return nil, err
		}
		out = append(out, domain.LogEntry{Time: at, Kind: "comment", Actor: by, Detail: excerpt(verContent[avid], start, end)})
	}
	crows.Close()
	if err := crows.Err(); err != nil {
		return nil, err
	}

	// suggestions (joined to the doc via comments) → proposal.
	srows, err := s.DB.Query(`SELECT sg.created_by, sg.created_at FROM suggestions sg JOIN comments c ON sg.comment_id=c.id WHERE c.doc_id=?`, docID)
	if err != nil {
		return nil, err
	}
	for srows.Next() {
		var by, at string
		if err := srows.Scan(&by, &at); err != nil {
			srows.Close()
			return nil, err
		}
		out = append(out, domain.LogEntry{Time: at, Kind: "proposal", Actor: by})
	}
	srows.Close()
	if err := srows.Err(); err != nil {
		return nil, err
	}

	// approvals → approval; the earliest is "approved", later ones "re-approved".
	// rowid tiebreak keeps same-second approvals in insertion order so the
	// first-vs-re-approval distinction is deterministic.
	arows, err := s.DB.Query(`SELECT version_id, approved_by, note, created_at FROM approvals WHERE doc_id=? ORDER BY created_at, rowid`, docID)
	if err != nil {
		return nil, err
	}
	firstApproval := true
	for arows.Next() {
		var vid, by, note, at string
		if err := arows.Scan(&vid, &by, &note, &at); err != nil {
			arows.Close()
			return nil, err
		}
		out = append(out, domain.LogEntry{Time: at, Kind: "approval", Actor: by, Detail: note, Version: verOrdinal[vid], ReApproval: !firstApproval})
		firstApproval = false
	}
	arows.Close()
	if err := arows.Err(); err != nil {
		return nil, err
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Time != out[j].Time {
			return out[i].Time < out[j].Time
		}
		return kindOrder[out[i].Kind] < kindOrder[out[j].Kind]
	})
	return out, nil
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `docker compose -f docker-compose.dev.yml exec -T backend go test ./internal/store/ -run TestListDecisionLog -v`
Expected: PASS.

- [ ] **Step 6: Run the whole module to confirm nothing else broke**

Run: `docker compose -f docker-compose.dev.yml exec -T backend go test ./...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/domain/domain.go internal/store/log.go internal/store/log_test.go
git commit -m "decision-log: LogEntry + ListDecisionLog (derived timeline)"
```

---

### Task 2: API endpoint `GET /api/docs/{id}/log`

**Files:**
- Modify: `internal/api/api.go`
- Test: `internal/api/api_test.go`

**Interfaces:**
- Consumes: `(*Store).ListDecisionLog(docID)` (Task 1); the existing `writeJSON(w, v, err)` helper in `api.go`.
- Produces: `GET /api/docs/{id}/log` → `[]domain.LogEntry` JSON (`[]` when empty).

- [ ] **Step 1: Write the failing test** — append to `internal/api/api_test.go` (package `api`; build the handler with `NewAPI(svc, s)` as the existing tests do). Add imports `"encoding/json"`, `"net/http/httptest"` if missing:

```go
func TestDecisionLogEndpoint(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _ string) error { return nil })
	h := NewAPI(svc, s)

	doc, v1, _ := s.CreateDocument("spec.md", "hello world", "human")
	_, _ = s.CreateComment(domain.Comment{
		DocID: doc.ID, AgainstVersionID: v1.ID, Anchor: domain.Anchor{Start: 6, End: 11},
		AuthorIdentity: "human", Owner: "human", Status: domain.CommentOpen,
	})

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/api/docs/"+doc.ID+"/log", nil))
	if rr.Code != 200 {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	var log []domain.LogEntry
	if err := json.Unmarshal(rr.Body.Bytes(), &log); err != nil {
		t.Fatal(err)
	}
	if len(log) != 2 || log[0].Kind != "created" || log[1].Kind != "comment" || log[1].Detail != "world" {
		t.Fatalf("log = %+v, want [created, comment(world)]", log)
	}
}
```

> If `domain` isn't already imported in `api_test.go`, add `"github.com/rajanrx/outbox-md/internal/domain"`.

- [ ] **Step 2: Run test to verify it fails**

Run: `docker compose -f docker-compose.dev.yml exec -T backend go test ./internal/api/ -run TestDecisionLogEndpoint -v`
Expected: FAIL (404 — route not registered).

- [ ] **Step 3: Add the route** — in `internal/api/api.go`, register next to the other `GET /api/docs/...` handlers:

```go
	mux.HandleFunc("GET /api/docs/{id}/log", func(w http.ResponseWriter, r *http.Request) {
		log, err := st.ListDecisionLog(r.PathValue("id"))
		writeJSON(w, log, err)
	})
```

- [ ] **Step 4: Run test to verify it passes**

Run: `docker compose -f docker-compose.dev.yml exec -T backend go test ./internal/api/ -run TestDecisionLogEndpoint -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/api.go internal/api/api_test.go
git commit -m "decision-log: GET /api/docs/{id}/log endpoint"
```

---

### Task 3: Frontend — History timeline panel

**Files:**
- Modify: `web/src/api.ts` (add `LogEntry` type + `getLog`)
- Create: `web/src/log/DecisionLog.tsx`
- Create: `web/src/log/decisionlog.css`
- Modify: `web/src/App.tsx` (History toggle + render the panel)

**Interfaces:**
- Consumes: `GET /api/docs/{id}/log` → `LogEntry[]` (Task 2).
- Produces: `getLog(id: string): Promise<LogEntry[]>`; `<DecisionLog docId entries onClose />`.

- [ ] **Step 1: Add the API client** — in `web/src/api.ts`, add:

```ts
export type LogEntry = {
  time: string;
  kind: "created" | "comment" | "proposal" | "edit" | "approval";
  actor: string;
  detail: string;
  version: number;
  reApproval: boolean;
};

export async function getLog(id: string): Promise<LogEntry[]> {
  return (await fetch(`/api/docs/${id}/log`)).json();
}
```

- [ ] **Step 2: Build the DecisionLog component** — create `web/src/log/DecisionLog.tsx`:

```tsx
import { useEffect, useState } from "react";
import { getLog, type LogEntry } from "../api";
import "./decisionlog.css";

const ICON: Record<LogEntry["kind"], string> = {
  created: "✦", comment: "💬", proposal: "✎", edit: "✦", approval: "✓",
};

function phrase(e: LogEntry): string {
  switch (e.kind) {
    case "created": return "created the document (v1)";
    case "comment": return e.detail ? `commented on “${e.detail}”` : "added a comment";
    case "proposal": return "proposed a change";
    case "edit": return `applied an edit → v${e.version}`;
    case "approval": {
      const base = e.reApproval ? `re-approved v${e.version}` : `approved v${e.version}`;
      return e.detail ? `${base} — “${e.detail}”` : base;
    }
  }
}

export function DecisionLog({ docId, onClose }: { docId: string; onClose: () => void }) {
  const [entries, setEntries] = useState<LogEntry[]>([]);
  useEffect(() => { getLog(docId).then((e) => setEntries(e ?? [])); }, [docId]);

  return (
    <div className="log-panel" onClick={(e) => e.stopPropagation()}>
      <div className="log-head">
        <span>History</span>
        <button className="log-close" onClick={onClose} aria-label="Close">×</button>
      </div>
      {entries.length === 0 ? (
        <div className="log-empty">No activity yet.</div>
      ) : (
        <ol className="log-list">
          {entries.map((e, i) => (
            <li key={i} className={`log-row kind-${e.kind}`}>
              <span className="log-ic" aria-hidden>{ICON[e.kind]}</span>
              <div className="log-body">
                <span className={`log-actor who-${e.actor}`}>{e.actor}</span> {phrase(e)}
                <div className="log-time">{e.time}</div>
              </div>
            </li>
          ))}
        </ol>
      )}
    </div>
  );
}
```

- [ ] **Step 3: Style it** — create `web/src/log/decisionlog.css`:

```css
.log-panel {
  position: absolute; top: 50px; right: 0; width: min(560px, 60vw); max-height: 72vh;
  z-index: 20; display: flex; flex-direction: column; overflow: hidden;
  background: var(--chrome-2); border: 1px solid var(--chrome-line);
  border-radius: 0 0 0 12px; box-shadow: 0 24px 60px -28px rgba(20,15,5,.4);
}
.log-head {
  display: flex; align-items: center; justify-content: space-between;
  padding: 9px 14px; font-size: 12px; font-weight: 650; color: var(--ink-soft);
  background: var(--chrome); border-bottom: 1px solid var(--chrome-line);
}
.log-close { width: 26px; height: 26px; border: 0; border-radius: 6px; background: none; font-size: 18px; color: var(--ink-soft); }
.log-close:hover { background: var(--chrome-3); color: var(--ink); }
.log-list { list-style: none; margin: 0; padding: 8px 0; overflow-y: auto; }
.log-row { display: flex; gap: 10px; padding: 8px 14px; align-items: flex-start; }
.log-ic { flex: none; width: 20px; text-align: center; color: var(--chrome-soft); }
.kind-approval .log-ic { color: var(--agent); }
.kind-comment .log-ic, .kind-proposal .log-ic { color: var(--accent); }
.log-body { font-size: 13px; line-height: 1.5; color: var(--ink); }
.log-actor { font-weight: 650; text-transform: capitalize; }
.who-human { color: var(--accent); }
.who-agent { color: var(--agent); }
.log-time { font-size: 11px; color: var(--chrome-soft); margin-top: 2px; font-variant-numeric: tabular-nums; }
.log-empty { padding: 24px; text-align: center; color: var(--chrome-soft); font-size: 13px; }
```

- [ ] **Step 4: Wire the History toggle into App** — in `web/src/App.tsx`:
  1. Add imports: extend the `./api` import (it already imports from `./api`) — no new symbol needed in App beyond the component; and `import { DecisionLog } from "./log/DecisionLog";`.
  2. Add state near the other panel state: `const [showLog, setShowLog] = useState(false);`.
  3. In the top bar, add a History toggle button next to the existing lifecycle controls (before the `.spacer`). Match the existing `icon-btn`/`gov-btn` styling — a small text button is fine:

```tsx
{view && (
  <button className="gov-btn ghost" onClick={() => setShowLog((v) => !v)}>History</button>
)}
```

  4. The top bar must be a positioning context for the panel. If the governance work already set `style={{ position: "relative" }}` on the `.topbar`, reuse it; otherwise add it. Render the panel just before the topbar closes:

```tsx
{showLog && view && (
  <DecisionLog docId={docId} onClose={() => setShowLog(false)} />
)}
```

> Read the current `web/src/App.tsx` first — place the button and panel to match the real top-bar markup (brand, breadcrumb, lifecycle controls, spacer, collapse icons). Do not duplicate an existing `position: relative`.

- [ ] **Step 5: Typecheck and build**

Run: `cd web && npx tsc --noEmit && npm run build`
Expected: no type errors; build succeeds.

- [ ] **Step 6: Live verification (localhost:5173)** — take a doc through the lifecycle (approve, comment, accept a suggestion, re-approve), click **History**, and confirm the timeline lists the events oldest-first with the right phrasing (created → comment → proposal → edit → approved/re-approved), actors coloured by identity, and timestamps. Confirm an untouched doc shows "No activity yet." beyond its `created` entry.

- [ ] **Step 7: Commit**

```bash
git add web/src/api.ts web/src/log/DecisionLog.tsx web/src/log/decisionlog.css web/src/App.tsx
git commit -m "decision-log: History timeline panel"
```

---

## Notes for the executor

- Store/API/service tests have NO shared helpers — each inlines `Open(":memory:")` / `store.Open(":memory:")` + `service.New(s, func(_, _ string) error { return nil })` + `NewAPI(svc, s)`. Follow the snippets above; don't invent helpers.
- `created_at` comes from `datetime('now')` (one-second resolution); in fast tests every event shares a timestamp, so the `(time, kind-priority)` sort is what makes order deterministic — the Task 1 test asserts exactly that.
- This is a pure read-view: do not add tables, columns, writes, or dependencies. If a task seems to need one, stop and report.
- After all tasks: final whole-branch review, then push and open a PR against `main` (do not merge).
