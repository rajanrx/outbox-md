# Governance Seam & Amendment Cycle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give documents a governed lifecycle (`draft → approved → amending → approved`) with a pinned approved baseline; post-approval edits accumulate as a pending amendment until a human re-approves.

**Architecture:** Add lifecycle/baseline state to `Document`, an append-only `Approval` history, and a `post_approval` flag on `Comment`. The accept-suggestion path branches on lifecycle: `draft` writes the file directly (today's behaviour); `approved`/`amending` advance the working head without touching disk, so the on-disk `.md` stays at the baseline until re-approve writes it. Approve/Re-approve are human-only HTTP endpoints; the frontend gains a lifecycle badge, Approve/Re-approve, and a diff-against-baseline view.

**Tech Stack:** Go 1.25 (`net/http`, `database/sql`, `modernc.org/sqlite`), MCP (Go SDK), React 19 + TypeScript + Vite, `diff-match-patch`.

**Spec:** `docs/specs/2026-06-28-governance-seam-design.md`

## Global Constraints

- Commit identity is `rajan <rajanrauniyar@gmail.com>`. NEVER add a `Co-Authored-By: Claude` trailer.
- Local-first, single-user, **unauthenticated**. Approve/Re-approve identity is **server-set** (`service.LocalHuman = "human"`), never taken from the request body. Do NOT add authentication.
- Lifecycle is `draft | approved | amending` only. There is **no `in_review` state**.
- **Guards are definite:** `approve` is valid only from `draft` (else rejected "already approved — use re-approve"); `reapprove` is valid only from `amending` with `currentVersionId != approvedVersionId` (else rejected "nothing to re-approve").
- **Disk invariant:** `draft` → on-disk file equals the current version; `approved`/`amending` → on-disk file equals the approved baseline. Accept on an approved/amending doc never writes disk; only Re-approve does.
- Approve is allowed even with open comments.
- Approval is **not** an MCP operation; agents cannot approve.
- Run Go tests in the dev backend container: `docker compose -f docker-compose.dev.yml exec -T backend go test ./...`. Run a single package with `... go test ./internal/<pkg>/ -run TestName -v`.
- Frontend typecheck: `cd web && npx tsc --noEmit`. Build: `cd web && npm run build`.

---

### Task 1: Domain types, schema, migration, and store reads

**Files:**
- Modify: `internal/domain/domain.go`
- Modify: `internal/store/schema.sql`
- Modify: `internal/store/store.go`
- Modify: `internal/store/documents.go` (`GetDocument`, `GetDocumentByPath`, `ListDocuments`)
- Modify: `internal/store/comments.go` (`commentCols`, `scanComment`, `CreateComment`)
- Test: `internal/store/documents_test.go`, `internal/store/comments_test.go`

**Interfaces:**
- Produces: `domain.DocumentStatus` (`DocDraft`/`DocApproved`/`DocAmending`); `domain.Document{…, Status, ApprovedVersionID}`; `domain.Approval{ID, DocID, VersionID, ApprovedBy, Note, CreatedAt}`; `domain.Comment{…, PostApproval bool}`. A freshly created document reads back `Status == DocDraft`, `ApprovedVersionID == ""`; a freshly created comment reads back `PostApproval == false` unless set.

- [ ] **Step 1: Write the failing test** — append to `internal/store/documents_test.go`, and add `"github.com/rajanrx/outbox-md/internal/domain"` to that file's import block (these tests are `package store`, so they call `Open` / `migrate` unqualified):

```go
func TestNewDocumentDefaultsToDraft(t *testing.T) {
	s, _ := Open(":memory:")
	defer s.Close()
	doc, _, err := s.CreateDocument("a.md", "hello", "human")
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetDocument(doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.DocDraft {
		t.Errorf("status = %q, want draft", got.Status)
	}
	if got.ApprovedVersionID != "" {
		t.Errorf("approvedVersionId = %q, want empty", got.ApprovedVersionID)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	s, _ := Open(":memory:")
	defer s.Close()
	// migrate already ran inside Open; running it again must be a no-op.
	if err := migrate(s.DB); err != nil {
		t.Fatalf("re-migrate: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `docker compose -f docker-compose.dev.yml exec -T backend go test ./internal/store/ -run TestNewDocumentDefaultsToDraft -v`
Expected: FAIL (compile error: `domain.DocDraft` undefined / `Status` field missing).

- [ ] **Step 3: Add domain types** — in `internal/domain/domain.go`, replace the `Document` struct and add the status type + `Approval`:

```go
type DocumentStatus string

const (
	DocDraft    DocumentStatus = "draft"
	DocApproved DocumentStatus = "approved"
	DocAmending DocumentStatus = "amending"
)

type Document struct {
	ID                string         `json:"id"`
	Path              string         `json:"path"`
	CurrentVersionID  string         `json:"currentVersionId"`
	Status            DocumentStatus `json:"status"`
	ApprovedVersionID string         `json:"approvedVersionId"`
}

type Approval struct {
	ID         string `json:"id"`
	DocID      string `json:"docId"`
	VersionID  string `json:"versionId"`
	ApprovedBy string `json:"approvedBy"`
	Note       string `json:"note"`
	CreatedAt  string `json:"createdAt"`
}
```

In the same file, add the `PostApproval` field to `Comment`:

```go
type Comment struct {
	ID               string        `json:"id"`
	DocID            string        `json:"docId"`
	AgainstVersionID string        `json:"againstVersionId"`
	Anchor           Anchor        `json:"anchor"`
	AuthorIdentity   string        `json:"authorIdentity"`
	Owner            string        `json:"owner"`
	Status           CommentStatus `json:"status"`
	PostApproval     bool          `json:"postApproval"`
	ClaimToken       string        `json:"-"`
}
```

- [ ] **Step 4: Update schema** — in `internal/store/schema.sql`, change the `documents` and `comments` CREATE statements and add the `approvals` table:

```sql
CREATE TABLE IF NOT EXISTS documents (
  id TEXT PRIMARY KEY,
  path TEXT NOT NULL UNIQUE,
  current_version_id TEXT,
  status TEXT NOT NULL DEFAULT 'draft',
  approved_version_id TEXT NOT NULL DEFAULT ''
);
```

```sql
CREATE TABLE IF NOT EXISTS comments (
  id TEXT PRIMARY KEY,
  doc_id TEXT NOT NULL REFERENCES documents(id),
  against_version_id TEXT NOT NULL REFERENCES versions(id),
  anchor_start INTEGER NOT NULL,
  anchor_end INTEGER NOT NULL,
  author_identity TEXT NOT NULL,
  owner TEXT NOT NULL,
  status TEXT NOT NULL,
  claim_token TEXT NOT NULL DEFAULT '',
  post_approval INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
```

Add after the `thread_messages` table:

```sql
CREATE TABLE IF NOT EXISTS approvals (
  id TEXT PRIMARY KEY,
  doc_id TEXT NOT NULL REFERENCES documents(id),
  version_id TEXT NOT NULL REFERENCES versions(id),
  approved_by TEXT NOT NULL,
  note TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
```

- [ ] **Step 5: Add migration for pre-existing DBs** — in `internal/store/store.go`, add `"strings"` to imports and a `migrate` call after the schema exec:

```go
func Open(dsn string) (*Store, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{DB: db}, nil
}

// migrate adds columns introduced after a database was first created. SQLite
// has no "ADD COLUMN IF NOT EXISTS", so a duplicate-column error is expected
// and ignored on databases that already have the column (CREATE TABLE above
// covers fresh databases).
func migrate(db *sql.DB) error {
	for _, stmt := range []string{
		`ALTER TABLE documents ADD COLUMN status TEXT NOT NULL DEFAULT 'draft'`,
		`ALTER TABLE documents ADD COLUMN approved_version_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE comments ADD COLUMN post_approval INTEGER NOT NULL DEFAULT 0`,
	} {
		if _, err := db.Exec(stmt); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 6: Update document read scans** — in `internal/store/documents.go`, update the three readers:

```go
func (s *Store) GetDocument(id string) (domain.Document, error) {
	var d domain.Document
	var cur *string
	err := s.DB.QueryRow(`SELECT id, path, current_version_id, status, approved_version_id FROM documents WHERE id=?`, id).
		Scan(&d.ID, &d.Path, &cur, &d.Status, &d.ApprovedVersionID)
	if cur != nil {
		d.CurrentVersionID = *cur
	}
	return d, err
}

func (s *Store) GetDocumentByPath(path string) (domain.Document, bool, error) {
	var d domain.Document
	var cur *string
	err := s.DB.QueryRow(`SELECT id, path, current_version_id, status, approved_version_id FROM documents WHERE path=?`, path).
		Scan(&d.ID, &d.Path, &cur, &d.Status, &d.ApprovedVersionID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Document{}, false, nil
	}
	if cur != nil {
		d.CurrentVersionID = *cur
	}
	return d, err == nil, err
}

func (s *Store) ListDocuments() ([]domain.Document, error) {
	rows, err := s.DB.Query(`SELECT id, path, COALESCE(current_version_id,''), status, approved_version_id FROM documents ORDER BY path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Document{}
	for rows.Next() {
		var d domain.Document
		if err := rows.Scan(&d.ID, &d.Path, &d.CurrentVersionID, &d.Status, &d.ApprovedVersionID); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
```

- [ ] **Step 7: Update comment store for `post_approval`** — in `internal/store/comments.go`, update `commentCols`, `scanComment`, and `CreateComment`. SQLite stores the flag as `INTEGER`; scan through an `int` because `database/sql` will not scan an integer straight into `*bool`:

```go
const commentCols = `id, doc_id, against_version_id, anchor_start, anchor_end,
	author_identity, owner, status, claim_token, post_approval`

func scanComment(scan func(...any) error) (domain.Comment, error) {
	var c domain.Comment
	var pa int
	err := scan(&c.ID, &c.DocID, &c.AgainstVersionID, &c.Anchor.Start, &c.Anchor.End,
		&c.AuthorIdentity, &c.Owner, &c.Status, &c.ClaimToken, &pa)
	c.PostApproval = pa != 0
	return c, err
}

func (s *Store) CreateComment(c domain.Comment) (domain.Comment, error) {
	if c.ID == "" {
		c.ID = domain.NewID()
	}
	pa := 0
	if c.PostApproval {
		pa = 1
	}
	_, err := s.DB.Exec(`INSERT INTO comments(`+commentCols+`) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		c.ID, c.DocID, c.AgainstVersionID, c.Anchor.Start, c.Anchor.End,
		c.AuthorIdentity, c.Owner, c.Status, c.ClaimToken, pa)
	return c, err
}
```

- [ ] **Step 8: Add a comment flag test** — append to `internal/store/comments_test.go` (ensure `"github.com/rajanrx/outbox-md/internal/domain"` is imported there):

```go
func TestCommentPostApprovalRoundTrips(t *testing.T) {
	s, _ := Open(":memory:")
	defer s.Close()
	doc, _, _ := s.CreateDocument("a.md", "hi", "human")
	c, err := s.CreateComment(domain.Comment{
		DocID: doc.ID, AgainstVersionID: doc.CurrentVersionID,
		Anchor: domain.Anchor{Start: 0, End: 1}, AuthorIdentity: "human",
		Owner: "human", Status: domain.CommentOpen, PostApproval: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetComment(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.PostApproval {
		t.Error("postApproval = false, want true")
	}
}
```

- [ ] **Step 9: Run tests to verify they pass**

Run: `docker compose -f docker-compose.dev.yml exec -T backend go test ./internal/store/ ./internal/domain/ -v`
Expected: PASS (all store + domain tests).

- [ ] **Step 10: Verify the whole module still builds and tests pass** (other packages reference these structs)

Run: `docker compose -f docker-compose.dev.yml exec -T backend go build ./... && docker compose -f docker-compose.dev.yml exec -T backend go test ./...`
Expected: PASS.

- [ ] **Step 11: Commit**

```bash
git add internal/domain/domain.go internal/store/schema.sql internal/store/store.go internal/store/documents.go internal/store/comments.go internal/store/documents_test.go internal/store/comments_test.go
git commit -m "governance: lifecycle/baseline fields, approvals schema, post_approval flag"
```

---

### Task 2: Approval store + document approval mutator

**Files:**
- Create: `internal/store/approvals.go`
- Modify: `internal/store/documents.go` (add `SetDocumentApproval`)
- Test: `internal/store/approvals_test.go`

**Interfaces:**
- Consumes: `domain.Approval`, `domain.DocumentStatus` (Task 1).
- Produces: `(*Store).CreateApproval(domain.Approval) (domain.Approval, error)`; `(*Store).ListApprovals(docID string) ([]domain.Approval, error)`; `(*Store).SetDocumentApproval(docID, approvedVersionID string, status domain.DocumentStatus) error`.

- [ ] **Step 1: Write the failing test** — create `internal/store/approvals_test.go`:

```go
package store

import (
	"testing"

	"github.com/rajanrx/outbox-md/internal/domain"
)

func TestSetDocumentApprovalAndCreateApproval(t *testing.T) {
	s, _ := Open(":memory:")
	defer s.Close()
	doc, _, _ := s.CreateDocument("a.md", "hi", "human")

	if err := s.SetDocumentApproval(doc.ID, doc.CurrentVersionID, domain.DocApproved); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetDocument(doc.ID)
	if got.Status != domain.DocApproved || got.ApprovedVersionID != doc.CurrentVersionID {
		t.Fatalf("doc = %+v, want approved baseline pinned", got)
	}

	if _, err := s.CreateApproval(domain.Approval{
		DocID: doc.ID, VersionID: doc.CurrentVersionID, ApprovedBy: "human", Note: "ok",
	}); err != nil {
		t.Fatal(err)
	}
	apps, err := s.ListApprovals(doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 1 || apps[0].VersionID != doc.CurrentVersionID {
		t.Fatalf("approvals = %+v, want one for the current version", apps)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `docker compose -f docker-compose.dev.yml exec -T backend go test ./internal/store/ -run TestSetDocumentApprovalAndCreateApproval -v`
Expected: FAIL (compile error: `SetDocumentApproval`/`CreateApproval`/`ListApprovals` undefined).

- [ ] **Step 3: Implement the approval store** — create `internal/store/approvals.go`:

```go
package store

import "github.com/rajanrx/outbox-md/internal/domain"

func (s *Store) CreateApproval(a domain.Approval) (domain.Approval, error) {
	if a.ID == "" {
		a.ID = domain.NewID()
	}
	_, err := s.DB.Exec(
		`INSERT INTO approvals(id, doc_id, version_id, approved_by, note) VALUES(?,?,?,?,?)`,
		a.ID, a.DocID, a.VersionID, a.ApprovedBy, a.Note)
	return a, err
}

func (s *Store) ListApprovals(docID string) ([]domain.Approval, error) {
	rows, err := s.DB.Query(
		`SELECT id, doc_id, version_id, approved_by, note, created_at
		 FROM approvals WHERE doc_id=? ORDER BY created_at`, docID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Approval{}
	for rows.Next() {
		var a domain.Approval
		if err := rows.Scan(&a.ID, &a.DocID, &a.VersionID, &a.ApprovedBy, &a.Note, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Implement the document mutator** — append to `internal/store/documents.go`:

```go
// SetDocumentApproval pins the approved baseline and sets the lifecycle status.
// Pass the same approvedVersionID to keep the baseline (e.g. status -> amending).
func (s *Store) SetDocumentApproval(docID, approvedVersionID string, status domain.DocumentStatus) error {
	_, err := s.DB.Exec(`UPDATE documents SET approved_version_id=?, status=? WHERE id=?`,
		approvedVersionID, status, docID)
	return err
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `docker compose -f docker-compose.dev.yml exec -T backend go test ./internal/store/ -run TestSetDocumentApprovalAndCreateApproval -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/store/approvals.go internal/store/documents.go internal/store/approvals_test.go
git commit -m "governance: approval store and document approval mutator"
```

---

### Task 3: Service Approve + Re-approve

**Files:**
- Modify: `internal/service/service.go` (add `Approve`, `Reapprove`)
- Test: `internal/service/service_test.go`

**Interfaces:**
- Consumes: `(*Store).SetDocumentApproval`, `(*Store).CreateApproval`, `(*Store).GetVersion` (Tasks 1–2); `service.LocalHuman`; `s.writeFile`.
- Produces: `(*Service).Approve(docID, note string) (domain.Approval, error)`; `(*Service).Reapprove(docID, note string) (domain.Approval, error)`.

- [ ] **Step 1: Write the failing test** — append to `internal/service/service_test.go` (these tests are `package service`, so they call `New(...)` directly; `store` and `domain` are already imported in this file):

```go
func TestApprovePinsBaseline(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := New(s, func(_, _ string) error { return nil })
	doc, _, _ := s.CreateDocument("a.md", "v1", "human")

	app, err := svc.Approve(doc.ID, "looks good")
	if err != nil {
		t.Fatal(err)
	}
	if app.ApprovedBy != "human" {
		t.Errorf("approvedBy = %q, want human", app.ApprovedBy)
	}
	got, _ := s.GetDocument(doc.ID)
	if got.Status != domain.DocApproved || got.ApprovedVersionID != doc.CurrentVersionID {
		t.Fatalf("doc = %+v, want approved at current version", got)
	}
	// Approving again is rejected — use re-approve.
	if _, err := svc.Approve(doc.ID, ""); err == nil {
		t.Error("second approve should be rejected")
	}
}

func TestReapproveRejectedWhenNothingPending(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := New(s, func(_, _ string) error { return nil })
	doc, _, _ := s.CreateDocument("a.md", "v1", "human")
	_, _ = svc.Approve(doc.ID, "")
	if _, err := svc.Reapprove(doc.ID, ""); err == nil {
		t.Error("reapprove on approved doc with no pending changes should be rejected")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `docker compose -f docker-compose.dev.yml exec -T backend go test ./internal/service/ -run TestApprovePinsBaseline -v`
Expected: FAIL (compile error: `svc.Approve` undefined).

- [ ] **Step 3: Implement Approve and Reapprove** — append to `internal/service/service.go`:

```go
// Approve pins the current version as the approved baseline. Valid only from
// draft; the on-disk file already equals the current version, so no rewrite is
// needed. Identity is server-set (LocalHuman), never taken from the request.
func (s *Service) Approve(docID, note string) (domain.Approval, error) {
	doc, err := s.store.GetDocument(docID)
	if err != nil {
		return domain.Approval{}, err
	}
	if doc.Status != domain.DocDraft {
		return domain.Approval{}, errors.New("already approved — use re-approve")
	}
	if err := s.store.SetDocumentApproval(docID, doc.CurrentVersionID, domain.DocApproved); err != nil {
		return domain.Approval{}, err
	}
	return s.store.CreateApproval(domain.Approval{
		DocID: docID, VersionID: doc.CurrentVersionID, ApprovedBy: LocalHuman, Note: note,
	})
}

// Reapprove advances the baseline to the working head and writes it to disk.
// Valid only while amending with pending changes ahead of the baseline.
func (s *Service) Reapprove(docID, note string) (domain.Approval, error) {
	doc, err := s.store.GetDocument(docID)
	if err != nil {
		return domain.Approval{}, err
	}
	if doc.Status != domain.DocAmending || doc.CurrentVersionID == doc.ApprovedVersionID {
		return domain.Approval{}, errors.New("nothing to re-approve")
	}
	ver, err := s.store.GetVersion(doc.CurrentVersionID)
	if err != nil {
		return domain.Approval{}, err
	}
	if err := s.writeFile(doc.Path, ver.Content); err != nil {
		return domain.Approval{}, err
	}
	if err := s.store.SetDocumentApproval(docID, doc.CurrentVersionID, domain.DocApproved); err != nil {
		return domain.Approval{}, err
	}
	return s.store.CreateApproval(domain.Approval{
		DocID: docID, VersionID: doc.CurrentVersionID, ApprovedBy: LocalHuman, Note: note,
	})
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `docker compose -f docker-compose.dev.yml exec -T backend go test ./internal/service/ -run 'TestApprovePinsBaseline|TestReapproveRejectedWhenNothingPending' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/service/service.go internal/service/service_test.go
git commit -m "governance: human-only Approve and Re-approve"
```

---

### Task 4: Post-approval comment flag + lifecycle-aware Accept

**Files:**
- Modify: `internal/service/service.go` (`PostComment`, `Accept`)
- Test: `internal/service/service_test.go`

**Interfaces:**
- Consumes: `(*Store).SetDocumentApproval` (Task 2); existing `(*Store).AddVersionTx` (note: passing a `nil` write callback skips the file write but keeps the compare-and-swap).
- Produces: `PostComment` sets `PostApproval` from lifecycle; `Accept` on an `approved`/`amending` doc advances the working head, sets `amending`, and does **not** write disk.

- [ ] **Step 1: Write the failing test** — append to `internal/service/service_test.go`. The fake `writeFile` captures the last content written and counts writes (same shape as the existing `TestAcceptRewritesFileAndReanchors`):

```go
func TestAcceptOnApprovedDocAccumulatesAmendmentWithoutWritingDisk(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	var written string
	writes := 0
	svc := New(s, func(_, content string) error { written = content; writes++; return nil })

	doc, _, _ := s.CreateDocument("a.md", "baseline", "human")
	if _, err := svc.Approve(doc.ID, ""); err != nil {
		t.Fatal(err)
	}

	// A post-approval comment is flagged.
	c, err := svc.PostComment(doc.ID, domain.Anchor{Start: 0, End: 1}, "human")
	if err != nil {
		t.Fatal(err)
	}
	if !c.PostApproval {
		t.Error("comment on approved doc should be flagged post_approval")
	}

	// Agent proposes a change; accepting it must NOT write disk or move the baseline.
	tok, _ := svc.Claim([]string{c.ID}, "agent")
	if _, err := svc.Propose(c.ID, tok, "amended baseline", "agent"); err != nil {
		t.Fatal(err)
	}
	writesBefore := writes
	if _, err := svc.Accept(c.ID); err != nil {
		t.Fatal(err)
	}
	if writes != writesBefore {
		t.Errorf("accept on approved doc wrote disk %d time(s); want 0", writes-writesBefore)
	}
	got, _ := s.GetDocument(doc.ID)
	if got.Status != domain.DocAmending {
		t.Errorf("status = %q, want amending", got.Status)
	}
	if got.ApprovedVersionID == got.CurrentVersionID {
		t.Error("baseline should not have advanced on accept")
	}

	// Re-approve advances the baseline and writes the head to disk.
	if _, err := svc.Reapprove(doc.ID, ""); err != nil {
		t.Fatal(err)
	}
	if written != "amended baseline" {
		t.Errorf("on-disk after reapprove = %q, want amended baseline", written)
	}
	got, _ = s.GetDocument(doc.ID)
	if got.Status != domain.DocApproved || got.ApprovedVersionID != got.CurrentVersionID {
		t.Errorf("after reapprove doc = %+v, want approved at head", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `docker compose -f docker-compose.dev.yml exec -T backend go test ./internal/service/ -run TestAcceptOnApprovedDocAccumulates -v`
Expected: FAIL (`PostComment` does not set the flag; `Accept` writes disk and leaves status approved).

- [ ] **Step 3: Flag post-approval comments** — in `internal/service/service.go`, replace `PostComment`:

```go
func (s *Service) PostComment(docID string, a domain.Anchor, author string) (domain.Comment, error) {
	doc, err := s.store.GetDocument(docID)
	if err != nil {
		return domain.Comment{}, err
	}
	return s.store.CreateComment(domain.Comment{
		DocID: docID, AgainstVersionID: doc.CurrentVersionID, Anchor: a,
		AuthorIdentity: author, Owner: author, Status: domain.CommentOpen,
		PostApproval: doc.Status == domain.DocApproved || doc.Status == domain.DocAmending,
	})
}
```

- [ ] **Step 4: Make Accept lifecycle-aware** — in `internal/service/service.go` `Accept`, after `oldVer` is fetched and before the `AddVersionTx` call, decide whether to write disk; replace the `wrote`/`AddVersionTx` block:

```go
	// draft writes the file directly; an approved/amending doc accumulates the
	// new version ahead of the baseline and leaves the on-disk file untouched
	// (the baseline) until re-approval.
	governed := doc.Status == domain.DocApproved || doc.Status == domain.DocAmending
	wrote := false
	var writeFn func(domain.Version) error
	if !governed {
		writeFn = func(v domain.Version) error {
			wrote = true
			return s.writeFile(doc.Path, v.Content)
		}
	}
	newVer, err := s.store.AddVersionTx(doc.ID, oldVer.ID, sg.ProposedContent, sg.CreatedBy, writeFn)
	if err != nil {
		if wrote {
			_ = s.writeFile(doc.Path, oldVer.Content)
		}
		if errors.Is(err, store.ErrVersionConflict) {
			_ = s.store.RejectSuggestionIfProposed(sg.ID)
			_ = s.store.ReopenCommentIfNotResolved(commentID)
		}
		return domain.Version{}, err
	}
	if governed {
		// keep the baseline; mark the doc as amending
		_ = s.store.SetDocumentApproval(doc.ID, doc.ApprovedVersionID, domain.DocAmending)
	}
```

Leave the rest of `Accept` (mark suggestion accepted, resolve the comment, rebase other comments) unchanged.

- [ ] **Step 5: Run tests to verify they pass**

Run: `docker compose -f docker-compose.dev.yml exec -T backend go test ./internal/service/ -v`
Expected: PASS (new test + all existing accept/concurrency tests — the draft path is unchanged).

- [ ] **Step 6: Run the whole suite under the race detector** (Accept is the concurrency-sensitive path)

Run: `docker compose -f docker-compose.dev.yml exec -T backend go test ./... -race`
Expected: PASS, no races.

- [ ] **Step 7: Commit**

```bash
git add internal/service/service.go internal/service/service_test.go
git commit -m "governance: flag post-approval comments; accept accumulates amendments without writing disk"
```

---

### Task 5: HTTP endpoints, baseline content, MCP read_doc exposure

**Files:**
- Modify: `internal/api/api.go` (add `approve`/`reapprove`; add `baselineContent` to `GET /api/docs/{id}`)
- Test: `internal/api/api_test.go`
- Test: `internal/mcp/handlers_test.go` (assert `read_doc` surfaces lifecycle)

**Interfaces:**
- Consumes: `(*Service).Approve`, `(*Service).Reapprove` (Task 3); `(*Store).GetVersion`.
- Produces: `POST /api/docs/{id}/approve` and `POST /api/docs/{id}/reapprove` (optional `{ "note": "…" }` body) returning the `domain.Approval` JSON; `GET /api/docs/{id}` response gains `baselineContent` (string; empty until approved). The `document` object in API and MCP responses now carries `status` and `approvedVersionId`.

- [ ] **Step 1: Write the failing test** — append to `internal/api/api_test.go` (this file is `package api`; it already imports `store` and `service` and builds the handler with `NewAPI(svc, s)` — see `TestDocAndCommentEndpoints`). Add imports `"net/http/httptest"`, `"strings"`, `"encoding/json"`, and `"github.com/rajanrx/outbox-md/internal/domain"` if not present:

```go
func TestApproveEndpointPinsBaseline(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _ string) error { return nil })
	h := NewAPI(svc, s)
	doc, _, _ := s.CreateDocument("a.md", "v1", "human")

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/docs/"+doc.ID+"/approve", strings.NewReader(`{"note":"ok"}`))
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("approve status = %d, body %s", rr.Code, rr.Body.String())
	}
	got, _ := s.GetDocument(doc.ID)
	if got.Status != domain.DocApproved {
		t.Errorf("status = %q, want approved", got.Status)
	}

	// Re-approve with nothing pending is a 400.
	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("POST", "/api/docs/"+doc.ID+"/reapprove", nil)
	h.ServeHTTP(rr2, req2)
	if rr2.Code != 400 {
		t.Errorf("reapprove status = %d, want 400", rr2.Code)
	}
}

func TestDocViewIncludesBaselineContent(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _ string) error { return nil })
	h := NewAPI(svc, s)
	doc, _, _ := s.CreateDocument("a.md", "v1", "human")
	rrA := httptest.NewRecorder()
	h.ServeHTTP(rrA, httptest.NewRequest("POST", "/api/docs/"+doc.ID+"/approve", nil))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/api/docs/"+doc.ID, nil))
	var view struct {
		BaselineContent string `json:"baselineContent"`
		Document        struct {
			Status string `json:"status"`
		} `json:"document"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.BaselineContent != "v1" {
		t.Errorf("baselineContent = %q, want v1", view.BaselineContent)
	}
	if view.Document.Status != "approved" {
		t.Errorf("status = %q, want approved", view.Document.Status)
	}
}
```

> Add imports `"net/http/httptest"`, `"strings"`, `"encoding/json"` if missing.

- [ ] **Step 2: Run test to verify it fails**

Run: `docker compose -f docker-compose.dev.yml exec -T backend go test ./internal/api/ -run 'TestApproveEndpoint|TestDocViewIncludesBaseline' -v`
Expected: FAIL (404 on `/approve`; `baselineContent` missing).

- [ ] **Step 3: Add baseline content to the doc view** — in `internal/api/api.go`, inside the `GET /api/docs/{id}` handler, after `comments` are loaded, replace the `writeJSON` call:

```go
		baseline := ""
		if doc.ApprovedVersionID != "" {
			if bv, err := st.GetVersion(doc.ApprovedVersionID); err == nil {
				baseline = bv.Content
			}
		}
		writeJSON(w, map[string]any{
			"document": doc, "content": ver.Content, "comments": comments, "baselineContent": baseline,
		}, nil)
```

- [ ] **Step 4: Add the approve/re-approve endpoints** — in `internal/api/api.go`, add (next to the other `POST /api/docs/...` handlers):

```go
	mux.HandleFunc("POST /api/docs/{id}/approve", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Note string `json:"note"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in) // body/note optional
		a, err := svc.Approve(r.PathValue("id"), in.Note)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, a, nil)
	})
	mux.HandleFunc("POST /api/docs/{id}/reapprove", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Note string `json:"note"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		a, err := svc.Reapprove(r.PathValue("id"), in.Note)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, a, nil)
	})
```

- [ ] **Step 5: Assert MCP read_doc surfaces lifecycle** — append to `internal/mcp/handlers_test.go` (this file is `package mcp` and already builds `&Handlers{Svc: svc, St: s}` — see `TestHandlersDriveTheLoop`). The `document` returned by `ReadDoc` is a `domain.Document` that now carries the new fields:

```go
func TestReadDocExposesLifecycle(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _ string) error { return nil })
	h := &Handlers{Svc: svc, St: s}
	doc, _, _ := s.CreateDocument("a.md", "v1", "human")
	_ = s.SetDocumentApproval(doc.ID, doc.CurrentVersionID, domain.DocApproved)

	out, err := h.ReadDoc(doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	d := out["document"].(domain.Document)
	if d.Status != domain.DocApproved || d.ApprovedVersionID != doc.CurrentVersionID {
		t.Errorf("read_doc document = %+v, want approved baseline", d)
	}
}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `docker compose -f docker-compose.dev.yml exec -T backend go test ./internal/api/ ./internal/mcp/ -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/api/api.go internal/api/api_test.go internal/mcp/handlers_test.go
git commit -m "governance: approve/reapprove endpoints, baseline content in doc view, read_doc lifecycle"
```

---

### Task 6: Frontend — lifecycle badge, Approve/Re-approve, baseline diff

**Files:**
- Create: `web/src/suggestion/diff.ts` (extract the shared `unifiedDiff` from `DiffPanel.tsx`)
- Modify: `web/src/suggestion/DiffPanel.tsx` (import `unifiedDiff` from `diff.ts`)
- Create: `web/src/governance/BaselineDiff.tsx`
- Create: `web/src/governance/governance.css`
- Modify: `web/src/api.ts` (types + `approve`/`reapprove`)
- Modify: `web/src/App.tsx` (badge + buttons + baseline-diff toggle)

**Interfaces:**
- Consumes: `GET /api/docs/{id}` now returns `{ document: { …, status, approvedVersionId }, content, comments, baselineContent }`; `POST /api/docs/{id}/approve`, `/reapprove`.
- Produces: `unifiedDiff(before: string, after: string): Row[]` exported from `web/src/suggestion/diff.ts` with `type Row = { op: "eq" | "ins" | "del" | "gap"; text: string }`.

- [ ] **Step 1: Extract the shared diff helper** — create `web/src/suggestion/diff.ts` by moving `unifiedDiff`, `CTX`, the `Row` type, and the `dmp` instance out of `DiffPanel.tsx`:

```ts
import { diff_match_patch } from "diff-match-patch";

const dmp = new diff_match_patch();
const CTX = 3; // context lines kept around each change

export type Row = { op: "eq" | "ins" | "del" | "gap"; text: string };

// A unified, line-based diff: only changed lines plus a few lines of context,
// long unchanged runs collapsed to a "… N unchanged lines" marker.
export function unifiedDiff(before: string, after: string): Row[] {
  const a = (dmp as any).diff_linesToChars_(before, after);
  const diffs = dmp.diff_main(a.chars1, a.chars2, false);
  (dmp as any).diff_charsToLines_(diffs, a.lineArray);

  const lines: Row[] = [];
  for (const [op, chunk] of diffs) {
    const parts = chunk.split("\n");
    if (parts.length && parts[parts.length - 1] === "") parts.pop();
    const kind = op === 1 ? "ins" : op === -1 ? "del" : "eq";
    for (const p of parts) lines.push({ op: kind, text: p });
  }

  const rows: Row[] = [];
  let i = 0;
  while (i < lines.length) {
    if (lines[i].op !== "eq") { rows.push(lines[i]); i++; continue; }
    let j = i;
    while (j < lines.length && lines[j].op === "eq") j++;
    const runLen = j - i;
    const showStart = i === 0 ? 0 : CTX;
    const showEnd = j === lines.length ? 0 : CTX;
    if (showStart + showEnd >= runLen) {
      for (let k = i; k < j; k++) rows.push(lines[k]);
    } else {
      for (let k = i; k < i + showStart; k++) rows.push(lines[k]);
      rows.push({ op: "gap", text: `… ${runLen - showStart - showEnd} unchanged lines` });
      for (let k = j - showEnd; k < j; k++) rows.push(lines[k]);
    }
    i = j;
  }
  return rows;
}
```

- [ ] **Step 2: Point DiffPanel at the shared helper** — in `web/src/suggestion/DiffPanel.tsx`, remove the local `dmp`, `CTX`, `Row`, and `unifiedDiff` definitions and add at the top:

```ts
import { unifiedDiff } from "./diff";
```

Keep everything else in `DiffPanel.tsx` unchanged.

- [ ] **Step 3: Verify the extraction compiles**

Run: `cd web && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 4: Extend the API client** — in `web/src/api.ts`, update the `DocView` type and add the two calls:

```ts
export type DocView = {
  document: { id: string; path: string; status: "draft" | "approved" | "amending"; approvedVersionId: string };
  content: string;
  comments: Comment[];
  baselineContent: string;
};

export async function approve(id: string, note = ""): Promise<unknown> {
  const r = await fetch(`/api/docs/${id}/approve`, { method: "POST", body: JSON.stringify({ note }) });
  return r.ok ? r.json() : null;
}
export async function reapprove(id: string, note = ""): Promise<unknown> {
  const r = await fetch(`/api/docs/${id}/reapprove`, { method: "POST", body: JSON.stringify({ note }) });
  return r.ok ? r.json() : null;
}
```

- [ ] **Step 5: Build the baseline diff view** — create `web/src/governance/BaselineDiff.tsx`:

```tsx
import { unifiedDiff } from "../suggestion/diff";
import "./governance.css";

export function BaselineDiff({ baseline, current, onClose }: {
  baseline: string;
  current: string;
  onClose: () => void;
}) {
  const rows = unifiedDiff(baseline, current);
  const changed = rows.some((r) => r.op === "ins" || r.op === "del");
  const sign = { eq: " ", ins: "+", del: "−", gap: "" } as const;
  return (
    <div className="baseline-diff">
      <div className="baseline-head">
        <span>Pending amendment vs approved baseline</span>
        <button className="baseline-close" onClick={onClose} aria-label="Close">×</button>
      </div>
      {changed ? (
        <div className="diff">
          {rows.map((r, i) => (
            <div key={i} className={`drow ${r.op}`}>
              <span className="sign">{sign[r.op]}</span>
              <span className="text">{r.text || " "}</span>
            </div>
          ))}
        </div>
      ) : (
        <div className="diff-empty">No changes ahead of the baseline yet.</div>
      )}
    </div>
  );
}
```

Create `web/src/governance/governance.css` (the `.diff`/`.drow` rules are reused from `suggestion/diff.css`, already imported globally via DiffPanel; this file styles only the wrapper):

```css
.lifecycle {
  display: inline-flex; align-items: center; gap: 6px; font-size: 11px; font-weight: 650;
  letter-spacing: .3px; text-transform: uppercase; padding: 3px 10px; border-radius: 20px;
  border: 1px solid var(--chrome-line); color: var(--chrome-soft);
}
.lifecycle::before { content: ""; width: 6px; height: 6px; border-radius: 50%; background: currentColor; }
.lifecycle.draft { color: var(--chrome-soft); }
.lifecycle.approved { color: var(--agent); border-color: rgba(43,140,127,.4); }
.lifecycle.amending { color: var(--accent); border-color: rgba(196,125,18,.4); }

.gov-btn {
  height: 30px; padding: 0 14px; border-radius: 8px; font-size: 12.5px; font-weight: 650;
  border: 1px solid transparent; background: var(--accent); color: var(--accent-ink);
}
.gov-btn:hover { filter: brightness(1.06); }
.gov-btn.ghost { background: var(--chrome-2); color: var(--ink); border-color: var(--chrome-line); }
.gov-btn.ghost:hover { background: var(--chrome-3); filter: none; }

.baseline-diff {
  position: absolute; top: 50px; right: 0; width: min(720px, 60vw); max-height: 70vh;
  z-index: 20; display: flex; flex-direction: column; overflow: hidden;
  background: var(--chrome-2); border: 1px solid var(--chrome-line);
  border-radius: 0 0 0 12px; box-shadow: 0 24px 60px -28px rgba(20,15,5,.4);
}
.baseline-head {
  display: flex; align-items: center; justify-content: space-between; gap: 8px;
  padding: 8px 12px; font-size: 12px; font-weight: 650; color: var(--ink-soft);
  background: var(--chrome); border-bottom: 1px solid var(--chrome-line);
}
.baseline-close { width: 26px; height: 26px; border: 0; border-radius: 6px; background: none; font-size: 18px; color: var(--ink-soft); }
.baseline-close:hover { background: var(--chrome-3); color: var(--ink); }
```

- [ ] **Step 6: Wire the badge, buttons, and baseline diff into App** — in `web/src/App.tsx`:
  1. Add imports: `import { approve, reapprove } from "./api";` (extend the existing `./api` import) and `import { BaselineDiff } from "./governance/BaselineDiff";` and `import "./governance/governance.css";`.
  2. Add state: `const [showBaseline, setShowBaseline] = useState(false);`.
  3. In the top bar, after the breadcrumb block and before the spacer, render the lifecycle controls:

```tsx
{view && (
  <div className="lifecycle-controls" style={{ display: "flex", alignItems: "center", gap: 10 }}>
    <span className={`lifecycle ${view.document.status}`}>{view.document.status}</span>
    {view.document.status === "draft" && (
      <button className="gov-btn" onClick={async () => { await approve(docId); refresh(); }}>Approve</button>
    )}
    {view.document.status === "amending" && (
      <>
        <button className="gov-btn ghost" onClick={() => setShowBaseline((v) => !v)}>View changes</button>
        <button className="gov-btn" onClick={async () => { await reapprove(docId); setShowBaseline(false); refresh(); }}>Re-approve</button>
      </>
    )}
  </div>
)}
```

  4. Make the `.topbar` a positioning context so the baseline panel anchors to it, and render the panel. Add `style={{ position: "relative" }}` to the topbar `div`, and just before the topbar closes, render:

```tsx
{showBaseline && view && (
  <BaselineDiff baseline={view.baselineContent} current={view.content} onClose={() => setShowBaseline(false)} />
)}
```

- [ ] **Step 7: Typecheck and build**

Run: `cd web && npx tsc --noEmit && npm run build`
Expected: no type errors; build succeeds.

- [ ] **Step 8: Live verification (Playwright or manual at localhost:5173)** — exercise the full loop against the running dev stack:
  1. Open a `draft` doc → top bar shows `DRAFT` + **Approve**.
  2. Click **Approve** → badge becomes `APPROVED`, Approve button disappears.
  3. Select text on the approved doc → add a comment → it is flagged post-approval; have an agent (dev endpoints) propose + accept a change → badge becomes `AMENDING`, **View changes** + **Re-approve** appear.
  4. Click **View changes** → baseline-diff panel shows the pending amendment vs baseline.
  5. Confirm the on-disk `.md` is still the baseline (unchanged) while amending: `git diff --stat docs/specs/<doc>.md` shows no change.
  6. Click **Re-approve** → badge returns to `APPROVED`; now the on-disk `.md` reflects the amended content.

- [ ] **Step 9: Commit**

```bash
git add web/src/suggestion/diff.ts web/src/suggestion/DiffPanel.tsx web/src/governance/BaselineDiff.tsx web/src/governance/governance.css web/src/api.ts web/src/App.tsx
git commit -m "governance: lifecycle badge, Approve/Re-approve, baseline diff view"
```

---

## Notes for the executor

- **There are no shared test helpers.** Every test inlines its setup: store tests (`package store`) use `Open(":memory:")`; service tests (`package service`) use `store.Open(":memory:")` + `New(s, writeFn)`; api tests use `store.Open` + `service.New` + `NewAPI(svc, s)`; mcp tests use `&Handlers{Svc: svc, St: s}`. The service fake `writeFile` is `func(_, content string) error { written = content; return nil }` — capture `written` (and a write counter) to assert disk behaviour. Follow these patterns; do not invent helpers.
- Keep the draft path of `Accept` byte-for-byte equivalent to today — the existing concurrency/CAS tests (`TestConcurrentAcceptsSerialize`, `TestAcceptRewritesFileAndReanchors`, `TestAcceptRejectsStaleSuggestion`, `TestDuplicateAcceptSameCommentStaysConsistent`, `TestAcceptRejectsRepeatedAccept`, `TestAcceptFailedWriteDoesNotAdvanceDB`) must stay green.
- The dev backend mounts the repo, so editing files on the branch is picked up by `air`; run tests with `docker compose -f docker-compose.dev.yml exec -T backend go test ./...`.
- After all tasks: run the final whole-branch review, then push and open a PR against `main` (do not merge).
