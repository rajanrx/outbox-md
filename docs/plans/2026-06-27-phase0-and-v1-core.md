# outbox-md — Phase 0 + v1-core Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stand up the public repo foundations, then build the *walking skeleton* — the irreducible loop that proves the core hypothesis: an inline comment stays correctly anchored across an AI-applied edit, and the document is never corrupted.

**Architecture:** A single Go binary owns a SQLite store, an HTTP/JSON API for the browser UI, and an MCP server (5 ops) for agents. The browser renders one Markdown file (CodeMirror, raw text), lets the user anchor a comment to a character range, lists the outbox, shows an agent's proposed replacement, and applies it on accept — creating a new immutable version, rewriting the file, and **re-anchoring open comments via a text diff**. Everything runs in one Docker container.

**Tech Stack:** Go 1.23 (`net/http`, `modernc.org/sqlite` pure-Go, `github.com/sergi/go-diff` for re-anchoring, official MCP Go SDK), React + Vite + TypeScript + CodeMirror 6 frontend, Docker (multi-stage, distroless).

## The hypothesis this plan must prove (v1-core)

> A comment anchored to a text range in version *N* re-anchors correctly when an agent's edit produces version *N+1* (the file is fully rewritten on accept), or is cleanly marked `detached` when its text was removed — with the on-disk file always equal to the latest version's content.

The **anchor re-mapping** task (Task 6) is the heart of the spike. If it cannot be made reliable on the test cases, stop and revisit the spec's anchoring model (§19 R1) before building further.

## Deliberate simplifications for the skeleton (revisit in v1-complete)

These are **intentional** — recorded so the implementer does not "helpfully" add them:

- **Editor:** CodeMirror 6 over raw Markdown, **not** TipTap. Char-offset anchors, not section-aware. The spike validates the anchoring model before committing to the richer editor.
- **Suggestions carry full replacement content**, not a diff. Simpler to apply; diff is computed for *display* only.
- **Live updates by polling** (frontend re-fetches), **not** WebSocket.
- **Single document focus** is fine; the API lists docs but the UI may open one at a time.
- **No** leases/reaper, config file, approval, provenance, links, dashboard, threads-beyond-one-reply, auth. All are v1-complete or later.

## Global Constraints

- **Go module path:** `github.com/rajanrx/outbox-md` (exact — baked into every import).
- **Go version:** 1.23 or higher.
- **SQLite driver:** `modernc.org/sqlite` (pure Go, **no CGO**). Builds must pass with `CGO_ENABLED=0`.
- **License:** MIT (already present at repo root; do not overwrite).
- **Commits:** Conventional Commits. Author is the repo owner; **never** add a `Co-Authored-By` trailer.
- **Generated state** lives under `.outbox/` and is gitignored; never commit it.
- **The on-disk `.md` file is always equal to the latest version's content** after any accept.
- **Anchors are rune offsets** (half-open `[start, end)`) into a specific version's content.

## File structure

```
go.mod
cmd/outbox-md/main.go              # entrypoint: load config, wire store+service+api+mcp, serve
internal/domain/domain.go          # core types: Document, Version, Comment, Suggestion, Anchor, enums
internal/anchor/anchor.go          # Remap(old,new,Anchor) → (Anchor, ok)   ← R1 core
internal/store/store.go            # Open(path) + migrations (embedded schema.sql)
internal/store/schema.sql          # SQLite DDL
internal/store/documents.go        # documents + versions persistence
internal/store/comments.go         # comments + thread messages persistence
internal/store/suggestions.go      # suggestions persistence
internal/service/service.go        # orchestration: PostComment, Claim, Propose, Reply, Accept
internal/api/api.go                # net/http handlers (JSON) for the UI
internal/mcp/mcp.go                # MCP server: 5 ops over service
web/                               # Vite React TS app
  package.json, vite.config.ts, tsconfig.json, index.html
  src/main.tsx, src/App.tsx
  src/api.ts                       # typed REST client
  src/lib/selection.ts             # CodeMirror selection → {start,end} rune offsets (pure, tested)
  src/components/Editor.tsx         # CodeMirror render + "comment on selection"
  src/components/Outbox.tsx         # list comments + statuses
  src/components/Suggestion.tsx     # show proposed diff + Accept
Dockerfile                         # multi-stage: build web, build go, distroless run
.dockerignore
.github/workflows/ci.yml           # go test/build + web build
README.md CONTRIBUTING.md CODE_OF_CONDUCT.md SECURITY.md CHANGELOG.md CODEOWNERS
.github/ISSUE_TEMPLATE/ , .github/pull_request_template.md
```

---

# Phase 0 — Foundations

## Task 0.1: Open-source repo scaffolding

**Files:**
- Create: `README.md`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `SECURITY.md`, `CHANGELOG.md`, `CODEOWNERS`, `.github/pull_request_template.md`, `.github/ISSUE_TEMPLATE/bug_report.md`, `.github/ISSUE_TEMPLATE/feature_request.md`
- Modify: `.gitignore` (ensure `.outbox/`, `node_modules/`, `web/dist/`, build artifacts)

This is a minor, mechanical task (docs only, no logic). No tests.

- [ ] **Step 1: Write `README.md`**

Include: one-line description, the "local-first, BYO-agent, zero-secrets" posture, a "Status: pre-alpha, walking skeleton" badge line, quickstart placeholder (`docker run`), and a link to `docs/specs/2026-06-27-outbox-md-design.md`.

- [ ] **Step 2: Write the governance files**

`CONTRIBUTING.md` (DCO sign-off: every commit needs `Signed-off-by`, Conventional Commits, how to run `go test ./...` and `npm --prefix web run build`). `CODE_OF_CONDUCT.md` (Contributor Covenant 2.1 text). `SECURITY.md` (how to privately report: email + "no public issues for vulns"; note the app exposes a local MCP endpoint and reads local files). `CHANGELOG.md` (Keep a Changelog header + `## [Unreleased]`). `CODEOWNERS` (`* @rajanrx`).

- [ ] **Step 3: Write the `.github` templates**

`pull_request_template.md` (summary / linked spec section / testing / DCO checkbox). Two `ISSUE_TEMPLATE` files with standard frontmatter.

- [ ] **Step 4: Update `.gitignore`**

Ensure these lines exist: `.outbox/`, `node_modules/`, `web/dist/`, `/outbox-md` (built binary), `*.test`, `.DS_Store`.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -s -m "docs: add open-source repo foundations (readme, governance, templates)"
```

## Task 0.2: Go module + health endpoint (TDD)

**Files:**
- Create: `go.mod`, `cmd/outbox-md/main.go`, `cmd/outbox-md/main_test.go`

**Interfaces:**
- Produces: `func newMux() http.Handler` returning a router with `GET /healthz` → `200 "ok"`. Reused by Task 16 (main wiring).

- [ ] **Step 1: Init the module**

```bash
go mod init github.com/rajanrx/outbox-md
```

- [ ] **Step 2: Write the failing test**

`cmd/outbox-md/main_test.go`:
```go
package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthz(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	newMux().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "ok" {
		t.Fatalf("body = %q, want \"ok\"", rec.Body.String())
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./cmd/...`
Expected: FAIL — `undefined: newMux`.

- [ ] **Step 4: Write minimal implementation**

`cmd/outbox-md/main.go`:
```go
package main

import (
	"log"
	"net/http"
	"os"
)

func newMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}

func main() {
	addr := ":8080"
	if v := os.Getenv("OUTBOX_ADDR"); v != "" {
		addr = v
	}
	log.Printf("outbox-md listening on %s", addr)
	if err := http.ListenAndServe(addr, newMux()); err != nil {
		log.Fatal(err)
	}
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `CGO_ENABLED=0 go test ./cmd/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod cmd/
git commit -s -m "feat: go module and /healthz endpoint"
```

## Task 0.3: Frontend scaffold (Vite + React + TS)

**Files:**
- Create: `web/package.json`, `web/vite.config.ts`, `web/tsconfig.json`, `web/index.html`, `web/src/main.tsx`, `web/src/App.tsx`

- [ ] **Step 1: Scaffold**

```bash
npm create vite@latest web -- --template react-ts
cd web && npm install
npm install -D vitest
```

(The skeleton editor is a plain `<textarea>`; CodeMirror/TipTap arrive in v1-complete, so no editor libraries are installed here.)

- [ ] **Step 2: Add a build-time API base + dev proxy**

`web/vite.config.ts` — add a dev server proxy so `/api` and `/mcp` reach the Go server (import `defineConfig` from `vitest/config` so the `test` field type-checks):
```ts
import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  server: { proxy: { "/api": "http://localhost:8080" } },
  build: { outDir: "dist" },
  test: { environment: "node" },
});
```

- [ ] **Step 3: Add a `test` script**

In `web/package.json` `scripts`, add: `"test": "vitest run"`.

- [ ] **Step 4: Replace `App.tsx` with a placeholder shell**

```tsx
export default function App() {
  return <div style={{ fontFamily: "system-ui", padding: 24 }}>outbox-md</div>;
}
```

- [ ] **Step 5: Verify build**

Run: `npm --prefix web run build`
Expected: builds to `web/dist` with no type errors.

- [ ] **Step 6: Commit**

```bash
git add web/
git commit -s -m "feat: scaffold vite react-ts frontend"
```

## Task 0.4: CI workflow

**Files:**
- Create: `.github/workflows/ci.yml`

- [ ] **Step 1: Write the workflow**

```yaml
name: ci
on:
  push: { branches: [main] }
  pull_request:
jobs:
  go:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: "1.23" }
      - run: CGO_ENABLED=0 go build ./...
      - run: CGO_ENABLED=0 go test ./...
      - run: go vet ./...
  web:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with: { node-version: "20" }
      - run: npm --prefix web ci
      - run: npm --prefix web run build
      - run: npm --prefix web test
```

- [ ] **Step 2: Commit**

```bash
git add .github/workflows/ci.yml
git commit -s -m "ci: build and test go + web on push and PR"
```

## Task 0.5: Dockerfile (multi-stage, CGO-free)

**Files:**
- Create: `Dockerfile`, `.dockerignore`

- [ ] **Step 1: Write `.dockerignore`**

```
web/node_modules
web/dist
.outbox
.git
*.test
```

- [ ] **Step 2: Write the `Dockerfile`**

```dockerfile
# --- web build ---
FROM node:20-alpine AS web
WORKDIR /web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# --- go build (embeds web/dist via Task 16) ---
FROM golang:1.23-alpine AS go
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
COPY --from=web /web/dist ./web/dist
RUN CGO_ENABLED=0 go build -o /outbox-md ./cmd/outbox-md

# --- runtime ---
FROM gcr.io/distroless/static-debian12
COPY --from=go /outbox-md /outbox-md
EXPOSE 8080
VOLUME ["/data"]
ENTRYPOINT ["/outbox-md"]
```

Note: the Go binary serves the SPA from an embedded `web/dist` (wired in Task 16). Until then, the image builds but serves only `/healthz`.

- [ ] **Step 3: Verify image builds**

Run: `docker build -t outbox-md:dev .`
Expected: image builds successfully.

- [ ] **Step 4: Commit**

```bash
git add Dockerfile .dockerignore
git commit -s -m "build: multi-stage cgo-free docker image"
```

---

# v1-core — Walking Skeleton

## Task 1: Domain types + ID helper (TDD)

**Files:**
- Create: `internal/domain/domain.go`, `internal/domain/id.go`, `internal/domain/id_test.go`

**Interfaces:**
- Produces (used by every later task):
  - `type Anchor struct { Start, End int }` — rune offsets, half-open `[Start,End)`.
  - `type Document struct { ID, Path, CurrentVersionID string }`
  - `type Version struct { ID, DocID string; Ordinal int; Content, CreatedBy string }`
  - `type Comment struct { ID, DocID, AgainstVersionID string; Anchor Anchor; AuthorIdentity, Owner string; Status CommentStatus; ClaimToken string }`
  - `type Suggestion struct { ID, CommentID, AgainstVersionID, ProposedContent string; State SuggestionState; CreatedBy string }`
  - `type ThreadMessage struct { ID, CommentID, AuthorIdentity, Body string }`
  - `CommentStatus` values: `open, claimed, addressed, replied, resolved, detached`. `SuggestionState` values: `proposed, accepted, rejected`.
  - `func NewID() string` — unique, URL-safe.

- [ ] **Step 1: Write the failing test**

`internal/domain/id_test.go`:
```go
package domain

import "testing"

func TestNewIDUniqueAndNonEmpty(t *testing.T) {
	a, b := NewID(), NewID()
	if a == "" || b == "" {
		t.Fatal("NewID returned empty")
	}
	if a == b {
		t.Fatalf("NewID not unique: %q == %q", a, b)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/domain/...`
Expected: FAIL — `undefined: NewID`.

- [ ] **Step 3: Write the implementation**

`internal/domain/id.go`:
```go
package domain

import (
	"crypto/rand"
	"encoding/hex"
)

func NewID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
```

`internal/domain/domain.go`:
```go
package domain

type Anchor struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

type Document struct {
	ID               string `json:"id"`
	Path             string `json:"path"`
	CurrentVersionID string `json:"currentVersionId"`
}

type Version struct {
	ID        string `json:"id"`
	DocID     string `json:"docId"`
	Ordinal   int    `json:"ordinal"`
	Content   string `json:"content"`
	CreatedBy string `json:"createdBy"`
}

type CommentStatus string

const (
	CommentOpen      CommentStatus = "open"
	CommentClaimed   CommentStatus = "claimed"
	CommentAddressed CommentStatus = "addressed"
	CommentReplied   CommentStatus = "replied"
	CommentResolved  CommentStatus = "resolved"
	CommentDetached  CommentStatus = "detached"
)

type Comment struct {
	ID               string        `json:"id"`
	DocID            string        `json:"docId"`
	AgainstVersionID string        `json:"againstVersionId"`
	Anchor           Anchor        `json:"anchor"`
	AuthorIdentity   string        `json:"authorIdentity"`
	Owner            string        `json:"owner"`
	Status           CommentStatus `json:"status"`
	ClaimToken       string        `json:"-"`
}

type SuggestionState string

const (
	SuggestionProposed SuggestionState = "proposed"
	SuggestionAccepted SuggestionState = "accepted"
	SuggestionRejected SuggestionState = "rejected"
)

type Suggestion struct {
	ID               string          `json:"id"`
	CommentID        string          `json:"commentId"`
	AgainstVersionID string          `json:"againstVersionId"`
	ProposedContent  string          `json:"proposedContent"`
	State            SuggestionState `json:"state"`
	CreatedBy        string          `json:"createdBy"`
}

type ThreadMessage struct {
	ID             string `json:"id"`
	CommentID      string `json:"commentId"`
	AuthorIdentity string `json:"authorIdentity"`
	Body           string `json:"body"`
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/domain/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/
git commit -s -m "feat: core domain types and id helper"
```

## Task 2: Anchor re-mapping — the R1 spike (TDD)

This is the **heart of the hypothesis**. Anchors are rune offsets into a version's content; `Remap` carries an anchor from old content to new content, or reports detachment.

**Files:**
- Create: `internal/anchor/anchor.go`, `internal/anchor/anchor_test.go`
- Modify: `go.mod` (adds `github.com/sergi/go-diff`)

**Interfaces:**
- Produces: `func Remap(oldContent, newContent string, a domain.Anchor) (domain.Anchor, bool)` — returns the remapped anchor and `true`, or `domain.Anchor{}, false` when the anchored text was removed or altered. Consumed by Task 7 (Accept).

- [ ] **Step 1: Add the diff dependency**

```bash
go get github.com/sergi/go-diff/diffmatchpatch
```

- [ ] **Step 2: Write the failing tests**

`internal/anchor/anchor_test.go`:
```go
package anchor

import (
	"testing"

	"github.com/rajanrx/outbox-md/internal/domain"
)

// "Hello world" → "world" is runes [6,11).
func TestRemap(t *testing.T) {
	const old = "Hello world"
	world := domain.Anchor{Start: 6, End: 11}

	cases := []struct {
		name     string
		newC     string
		want     domain.Anchor
		wantOK   bool
	}{
		{"no change", "Hello world", domain.Anchor{Start: 6, End: 11}, true},
		{"insert before", "Say Hello world", domain.Anchor{Start: 10, End: 15}, true},
		{"append after", "Hello world!!!", domain.Anchor{Start: 6, End: 11}, true},
		{"anchored text replaced", "Hello there", domain.Anchor{}, false},
		{"anchored text deleted", "Hello ", domain.Anchor{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := Remap(old, c.newC, world)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v", ok, c.wantOK)
			}
			if ok && got != c.want {
				t.Fatalf("anchor = %+v, want %+v", got, c.want)
			}
		})
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/anchor/...`
Expected: FAIL — `undefined: Remap`.

- [ ] **Step 4: Write the implementation**

`internal/anchor/anchor.go`:
```go
package anchor

import (
	"github.com/rajanrx/outbox-md/internal/domain"
	"github.com/sergi/go-diff/diffmatchpatch"
)

// Remap carries an anchor from oldContent to newContent.
// ok=false means the anchored text was removed or altered (detached).
func Remap(oldContent, newContent string, a domain.Anchor) (domain.Anchor, bool) {
	dmp := diffmatchpatch.New()
	diffs := dmp.DiffMain(oldContent, newContent, false)
	ns, ok1 := mapPos(diffs, a.Start)
	ne, ok2 := mapPos(diffs, a.End)
	if !ok1 || !ok2 || ne <= ns {
		return domain.Anchor{}, false
	}
	out := domain.Anchor{Start: ns, End: ne}
	// Detachment guard: the anchored text must be preserved verbatim.
	if sub(newContent, out) != sub(oldContent, a) {
		return domain.Anchor{}, false
	}
	return out, true
}

// mapPos maps a rune position in text1 to text2. ok=false if it falls
// inside deleted text.
func mapPos(diffs []diffmatchpatch.Diff, pos int) (int, bool) {
	oldPos, newPos := 0, 0
	for _, d := range diffs {
		n := len([]rune(d.Text))
		switch d.Type {
		case diffmatchpatch.DiffEqual:
			if pos <= oldPos+n {
				return newPos + (pos - oldPos), true
			}
			oldPos += n
			newPos += n
		case diffmatchpatch.DiffDelete:
			if pos < oldPos+n {
				return newPos, false
			}
			oldPos += n
		case diffmatchpatch.DiffInsert:
			newPos += n
		}
	}
	if pos == oldPos {
		return newPos, true
	}
	return newPos, false
}

func sub(s string, a domain.Anchor) string {
	r := []rune(s)
	if a.Start < 0 || a.End > len(r) || a.Start > a.End {
		return ""
	}
	return string(r[a.Start:a.End])
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/anchor/...`
Expected: PASS (all 5 sub-cases).

- [ ] **Step 6: Commit**

```bash
git add internal/anchor/ go.mod go.sum
git commit -s -m "feat: anchor re-mapping across edits (R1 spike core)"
```

> **Spike gate:** if any case cannot be made to pass reliably, STOP and revisit §19 R1 in the spec before continuing. This is the whole point of the skeleton.

## Task 3: Store — open + schema (TDD)

**Files:**
- Create: `internal/store/store.go`, `internal/store/schema.sql`, `internal/store/store_test.go`
- Modify: `go.mod` (adds `modernc.org/sqlite`)

**Interfaces:**
- Produces: `type Store struct { DB *sql.DB }`, `func Open(dsn string) (*Store, error)`, `func (s *Store) Close() error`. `Open` applies the embedded schema (idempotent). Use dsn `":memory:"` in tests, `file:<path>` in prod.

- [ ] **Step 1: Add the driver**

```bash
go get modernc.org/sqlite
```

- [ ] **Step 2: Write the schema**

`internal/store/schema.sql`:
```sql
CREATE TABLE IF NOT EXISTS documents (
  id TEXT PRIMARY KEY,
  path TEXT NOT NULL UNIQUE,
  current_version_id TEXT
);
CREATE TABLE IF NOT EXISTS versions (
  id TEXT PRIMARY KEY,
  doc_id TEXT NOT NULL REFERENCES documents(id),
  ordinal INTEGER NOT NULL,
  content TEXT NOT NULL,
  created_by TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
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
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS suggestions (
  id TEXT PRIMARY KEY,
  comment_id TEXT NOT NULL REFERENCES comments(id),
  against_version_id TEXT NOT NULL REFERENCES versions(id),
  proposed_content TEXT NOT NULL,
  state TEXT NOT NULL,
  created_by TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS thread_messages (
  id TEXT PRIMARY KEY,
  comment_id TEXT NOT NULL REFERENCES comments(id),
  author_identity TEXT NOT NULL,
  body TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
```

- [ ] **Step 3: Write the failing test**

`internal/store/store_test.go`:
```go
package store

import "testing"

func TestOpenCreatesTables(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var n int
	row := s.DB.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN
		 ('documents','versions','comments','suggestions','thread_messages')`)
	if err := row.Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Fatalf("tables = %d, want 5", n)
	}
}
```

- [ ] **Step 4: Run test to verify it fails**

Run: `go test ./internal/store/...`
Expected: FAIL — `undefined: Open`.

- [ ] **Step 5: Write the implementation**

`internal/store/store.go`:
```go
package store

import (
	"database/sql"
	_ "embed"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

type Store struct{ DB *sql.DB }

func Open(dsn string) (*Store, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{DB: db}, nil
}

func (s *Store) Close() error { return s.DB.Close() }
```

- [ ] **Step 6: Run test to verify it passes**

Run: `CGO_ENABLED=0 go test ./internal/store/...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/store/ go.mod go.sum
git commit -s -m "feat: sqlite store open + schema migration"
```

## Task 4: Store — documents & versions (TDD)

**Files:**
- Create: `internal/store/documents.go`, `internal/store/documents_test.go`

**Interfaces:**
- Produces:
  - `func (s *Store) CreateDocument(path, content, createdBy string) (domain.Document, domain.Version, error)` — creates the doc plus version ordinal 1, sets `current_version_id`.
  - `func (s *Store) GetDocument(id string) (domain.Document, error)`
  - `func (s *Store) ListDocuments() ([]domain.Document, error)`
  - `func (s *Store) GetVersion(id string) (domain.Version, error)`
  - `func (s *Store) AddVersion(docID, content, createdBy string) (domain.Version, error)` — ordinal = current max + 1, updates `current_version_id`.

- [ ] **Step 1: Write the failing test**

`internal/store/documents_test.go`:
```go
package store

import "testing"

func TestCreateAndVersion(t *testing.T) {
	s, _ := Open(":memory:")
	defer s.Close()

	doc, v1, err := s.CreateDocument("spec.md", "hello", "human")
	if err != nil {
		t.Fatal(err)
	}
	if v1.Ordinal != 1 || v1.Content != "hello" {
		t.Fatalf("v1 = %+v", v1)
	}
	if doc.CurrentVersionID != v1.ID {
		t.Fatal("current version not set to v1")
	}

	v2, err := s.AddVersion(doc.ID, "hello world", "agent")
	if err != nil {
		t.Fatal(err)
	}
	if v2.Ordinal != 2 {
		t.Fatalf("v2 ordinal = %d, want 2", v2.Ordinal)
	}
	got, _ := s.GetDocument(doc.ID)
	if got.CurrentVersionID != v2.ID {
		t.Fatal("current version not advanced to v2")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestCreateAndVersion`
Expected: FAIL — `undefined: CreateDocument`.

- [ ] **Step 3: Write the implementation**

`internal/store/documents.go`:
```go
package store

import "github.com/rajanrx/outbox-md/internal/domain"

func (s *Store) CreateDocument(path, content, createdBy string) (domain.Document, domain.Version, error) {
	docID := domain.NewID()
	verID := domain.NewID()
	tx, err := s.DB.Begin()
	if err != nil {
		return domain.Document{}, domain.Version{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO documents(id, path, current_version_id) VALUES(?,?,?)`,
		docID, path, verID); err != nil {
		return domain.Document{}, domain.Version{}, err
	}
	if _, err := tx.Exec(`INSERT INTO versions(id, doc_id, ordinal, content, created_by) VALUES(?,?,?,?,?)`,
		verID, docID, 1, content, createdBy); err != nil {
		return domain.Document{}, domain.Version{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Document{}, domain.Version{}, err
	}
	doc := domain.Document{ID: docID, Path: path, CurrentVersionID: verID}
	ver := domain.Version{ID: verID, DocID: docID, Ordinal: 1, Content: content, CreatedBy: createdBy}
	return doc, ver, nil
}

func (s *Store) GetDocument(id string) (domain.Document, error) {
	var d domain.Document
	var cur *string
	err := s.DB.QueryRow(`SELECT id, path, current_version_id FROM documents WHERE id=?`, id).
		Scan(&d.ID, &d.Path, &cur)
	if cur != nil {
		d.CurrentVersionID = *cur
	}
	return d, err
}

func (s *Store) ListDocuments() ([]domain.Document, error) {
	rows, err := s.DB.Query(`SELECT id, path, COALESCE(current_version_id,'') FROM documents ORDER BY path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Document
	for rows.Next() {
		var d domain.Document
		if err := rows.Scan(&d.ID, &d.Path, &d.CurrentVersionID); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) GetVersion(id string) (domain.Version, error) {
	var v domain.Version
	err := s.DB.QueryRow(
		`SELECT id, doc_id, ordinal, content, created_by FROM versions WHERE id=?`, id).
		Scan(&v.ID, &v.DocID, &v.Ordinal, &v.Content, &v.CreatedBy)
	return v, err
}

func (s *Store) AddVersion(docID, content, createdBy string) (domain.Version, error) {
	verID := domain.NewID()
	tx, err := s.DB.Begin()
	if err != nil {
		return domain.Version{}, err
	}
	defer tx.Rollback()
	var maxOrd int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(ordinal),0) FROM versions WHERE doc_id=?`, docID).
		Scan(&maxOrd); err != nil {
		return domain.Version{}, err
	}
	ord := maxOrd + 1
	if _, err := tx.Exec(`INSERT INTO versions(id, doc_id, ordinal, content, created_by) VALUES(?,?,?,?,?)`,
		verID, docID, ord, content, createdBy); err != nil {
		return domain.Version{}, err
	}
	if _, err := tx.Exec(`UPDATE documents SET current_version_id=? WHERE id=?`, verID, docID); err != nil {
		return domain.Version{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Version{}, err
	}
	return domain.Version{ID: verID, DocID: docID, Ordinal: ord, Content: content, CreatedBy: createdBy}, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `CGO_ENABLED=0 go test ./internal/store/ -run TestCreateAndVersion`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/
git commit -s -m "feat: document and version persistence"
```

## Task 5: Store — comments, suggestions, thread messages (TDD)

**Files:**
- Create: `internal/store/comments.go`, `internal/store/suggestions.go`, `internal/store/comments_test.go`

**Interfaces:**
- Produces (comments):
  - `func (s *Store) CreateComment(c domain.Comment) (domain.Comment, error)` (sets ID if empty)
  - `func (s *Store) GetComment(id string) (domain.Comment, error)`
  - `func (s *Store) ListComments(docID string) ([]domain.Comment, error)`
  - `func (s *Store) ListOpenComments() ([]domain.Comment, error)` (status = `open`, ordered by `created_at`)
  - `func (s *Store) UpdateCommentStatus(id string, status domain.CommentStatus, claimToken string) error`
  - `func (s *Store) UpdateCommentAnchor(id string, a domain.Anchor, status domain.CommentStatus) error`
  - `func (s *Store) AddThreadMessage(m domain.ThreadMessage) (domain.ThreadMessage, error)`
- Produces (suggestions):
  - `func (s *Store) CreateSuggestion(sg domain.Suggestion) (domain.Suggestion, error)`
  - `func (s *Store) GetSuggestionByComment(commentID string) (domain.Suggestion, bool, error)`
  - `func (s *Store) UpdateSuggestionState(id string, state domain.SuggestionState) error`

- [ ] **Step 1: Write the failing test**

`internal/store/comments_test.go`:
```go
package store

import (
	"testing"

	"github.com/rajanrx/outbox-md/internal/domain"
)

func TestCommentAndSuggestionRoundTrip(t *testing.T) {
	s, _ := Open(":memory:")
	defer s.Close()
	doc, v1, _ := s.CreateDocument("spec.md", "hello world", "human")

	c, err := s.CreateComment(domain.Comment{
		DocID: doc.ID, AgainstVersionID: v1.ID,
		Anchor: domain.Anchor{Start: 6, End: 11},
		AuthorIdentity: "human", Owner: "human", Status: domain.CommentOpen,
	})
	if err != nil {
		t.Fatal(err)
	}
	open, _ := s.ListOpenComments()
	if len(open) != 1 || open[0].ID != c.ID {
		t.Fatalf("open comments = %+v", open)
	}

	if _, err := s.CreateSuggestion(domain.Suggestion{
		CommentID: c.ID, AgainstVersionID: v1.ID,
		ProposedContent: "hello there", State: domain.SuggestionProposed, CreatedBy: "agent",
	}); err != nil {
		t.Fatal(err)
	}
	sg, ok, _ := s.GetSuggestionByComment(c.ID)
	if !ok || sg.ProposedContent != "hello there" {
		t.Fatalf("suggestion = %+v ok=%v", sg, ok)
	}

	if err := s.UpdateCommentAnchor(c.ID, domain.Anchor{Start: 6, End: 11}, domain.CommentDetached); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetComment(c.ID)
	if got.Status != domain.CommentDetached {
		t.Fatalf("status = %s, want detached", got.Status)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestCommentAndSuggestionRoundTrip`
Expected: FAIL — `undefined: CreateComment`.

- [ ] **Step 3: Write the implementations**

`internal/store/comments.go`:
```go
package store

import "github.com/rajanrx/outbox-md/internal/domain"

func scanComment(scan func(...any) error) (domain.Comment, error) {
	var c domain.Comment
	err := scan(&c.ID, &c.DocID, &c.AgainstVersionID, &c.Anchor.Start, &c.Anchor.End,
		&c.AuthorIdentity, &c.Owner, &c.Status, &c.ClaimToken)
	return c, err
}

const commentCols = `id, doc_id, against_version_id, anchor_start, anchor_end,
	author_identity, owner, status, claim_token`

func (s *Store) CreateComment(c domain.Comment) (domain.Comment, error) {
	if c.ID == "" {
		c.ID = domain.NewID()
	}
	_, err := s.DB.Exec(`INSERT INTO comments(`+commentCols+`) VALUES(?,?,?,?,?,?,?,?,?)`,
		c.ID, c.DocID, c.AgainstVersionID, c.Anchor.Start, c.Anchor.End,
		c.AuthorIdentity, c.Owner, c.Status, c.ClaimToken)
	return c, err
}

func (s *Store) GetComment(id string) (domain.Comment, error) {
	return scanComment(s.DB.QueryRow(`SELECT `+commentCols+` FROM comments WHERE id=?`, id).Scan)
}

func (s *Store) ListComments(docID string) ([]domain.Comment, error) {
	rows, err := s.DB.Query(`SELECT `+commentCols+` FROM comments WHERE doc_id=? ORDER BY created_at`, docID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Comment
	for rows.Next() {
		c, err := scanComment(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) ListOpenComments() ([]domain.Comment, error) {
	rows, err := s.DB.Query(`SELECT `+commentCols+` FROM comments WHERE status='open' ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Comment
	for rows.Next() {
		c, err := scanComment(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) UpdateCommentStatus(id string, status domain.CommentStatus, claimToken string) error {
	_, err := s.DB.Exec(`UPDATE comments SET status=?, claim_token=? WHERE id=?`, status, claimToken, id)
	return err
}

func (s *Store) UpdateCommentAnchor(id string, a domain.Anchor, status domain.CommentStatus) error {
	_, err := s.DB.Exec(`UPDATE comments SET anchor_start=?, anchor_end=?, status=? WHERE id=?`,
		a.Start, a.End, status, id)
	return err
}

func (s *Store) AddThreadMessage(m domain.ThreadMessage) (domain.ThreadMessage, error) {
	if m.ID == "" {
		m.ID = domain.NewID()
	}
	_, err := s.DB.Exec(`INSERT INTO thread_messages(id, comment_id, author_identity, body) VALUES(?,?,?,?)`,
		m.ID, m.CommentID, m.AuthorIdentity, m.Body)
	return m, err
}
```

`internal/store/suggestions.go`:
```go
package store

import (
	"database/sql"
	"errors"

	"github.com/rajanrx/outbox-md/internal/domain"
)

func (s *Store) CreateSuggestion(sg domain.Suggestion) (domain.Suggestion, error) {
	if sg.ID == "" {
		sg.ID = domain.NewID()
	}
	_, err := s.DB.Exec(
		`INSERT INTO suggestions(id, comment_id, against_version_id, proposed_content, state, created_by)
		 VALUES(?,?,?,?,?,?)`,
		sg.ID, sg.CommentID, sg.AgainstVersionID, sg.ProposedContent, sg.State, sg.CreatedBy)
	return sg, err
}

func (s *Store) GetSuggestionByComment(commentID string) (domain.Suggestion, bool, error) {
	var sg domain.Suggestion
	err := s.DB.QueryRow(
		`SELECT id, comment_id, against_version_id, proposed_content, state, created_by
		 FROM suggestions WHERE comment_id=? ORDER BY created_at DESC LIMIT 1`, commentID).
		Scan(&sg.ID, &sg.CommentID, &sg.AgainstVersionID, &sg.ProposedContent, &sg.State, &sg.CreatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Suggestion{}, false, nil
	}
	return sg, err == nil, err
}

func (s *Store) UpdateSuggestionState(id string, state domain.SuggestionState) error {
	_, err := s.DB.Exec(`UPDATE suggestions SET state=? WHERE id=?`, state, id)
	return err
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `CGO_ENABLED=0 go test ./internal/store/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/
git commit -s -m "feat: comment, suggestion, and thread-message persistence"
```

---

## Task 6: Service — the loop, incl. Accept + re-anchor (TDD)

This task wires the store and the anchor remapper into the actual workflow. **Accept is the integration proof of the hypothesis**: it creates a new version, rewrites the file, and re-anchors every other live comment.

**Files:**
- Create: `internal/service/service.go`, `internal/service/service_test.go`
- Modify: `internal/store/comments.go` (add `RebaseComment`)

**Interfaces:**
- Produces:
  - `func New(st *store.Store, writeFile func(path, content string) error) *Service`
  - `func (s *Service) PostComment(docID string, a domain.Anchor, author string) (domain.Comment, error)`
  - `func (s *Service) Claim(commentIDs []string, agent string) (token string, err error)`
  - `func (s *Service) Propose(commentID, token, content, agent string) (domain.Suggestion, error)`
  - `func (s *Service) Reply(commentID, token, body, agent string) error`
  - `func (s *Service) Accept(commentID string) (domain.Version, error)`
- Modify store: `func (s *Store) RebaseComment(id, newVersionID string, a domain.Anchor, status domain.CommentStatus) error`

- [ ] **Step 1: Add `RebaseComment` to the store**

Append to `internal/store/comments.go`:
```go
func (s *Store) RebaseComment(id, newVersionID string, a domain.Anchor, status domain.CommentStatus) error {
	_, err := s.DB.Exec(
		`UPDATE comments SET against_version_id=?, anchor_start=?, anchor_end=?, status=? WHERE id=?`,
		newVersionID, a.Start, a.End, status, id)
	return err
}
```

- [ ] **Step 2: Write the failing test (the hypothesis)**

`internal/service/service_test.go`:
```go
package service

import (
	"testing"

	"github.com/rajanrx/outbox-md/internal/domain"
	"github.com/rajanrx/outbox-md/internal/store"
)

func TestAcceptRewritesFileAndReanchors(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	var written string
	svc := New(s, func(_, content string) error { written = content; return nil })

	doc, _, _ := s.CreateDocument("spec.md", "Hello world", "human")
	cWorld, _ := svc.PostComment(doc.ID, domain.Anchor{Start: 6, End: 11}, "human") // "world"
	cHello, _ := svc.PostComment(doc.ID, domain.Anchor{Start: 0, End: 5}, "human")  // "Hello"

	tok, _ := svc.Claim([]string{cHello.ID}, "agent")
	if _, err := svc.Propose(cHello.ID, tok, "Say Hello world", "agent"); err != nil {
		t.Fatal(err)
	}
	nv, err := svc.Accept(cHello.ID)
	if err != nil {
		t.Fatal(err)
	}

	if nv.Content != "Say Hello world" || written != "Say Hello world" {
		t.Fatalf("content=%q written=%q", nv.Content, written)
	}
	// The OTHER comment must follow its text from [6,11) to [10,15).
	gotWorld, _ := s.GetComment(cWorld.ID)
	if gotWorld.Anchor != (domain.Anchor{Start: 10, End: 15}) {
		t.Fatalf("world anchor = %+v, want {10,15}", gotWorld.Anchor)
	}
	if gotWorld.Status != domain.CommentOpen {
		t.Fatalf("world status = %s, want open", gotWorld.Status)
	}
	gotHello, _ := s.GetComment(cHello.ID)
	if gotHello.Status != domain.CommentResolved {
		t.Fatalf("hello status = %s, want resolved", gotHello.Status)
	}
}

func TestProposeRejectsBadToken(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := New(s, func(_, _ string) error { return nil })
	doc, _, _ := s.CreateDocument("spec.md", "Hello world", "human")
	c, _ := svc.PostComment(doc.ID, domain.Anchor{Start: 0, End: 5}, "human")
	if _, err := svc.Propose(c.ID, "wrong-token", "x", "agent"); err == nil {
		t.Fatal("expected error for invalid claim token")
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/service/...`
Expected: FAIL — `undefined: New`.

- [ ] **Step 4: Write the implementation**

`internal/service/service.go`:
```go
package service

import (
	"errors"

	"github.com/rajanrx/outbox-md/internal/anchor"
	"github.com/rajanrx/outbox-md/internal/domain"
	"github.com/rajanrx/outbox-md/internal/store"
)

type Service struct {
	store     *store.Store
	writeFile func(path, content string) error
}

func New(st *store.Store, writeFile func(path, content string) error) *Service {
	return &Service{store: st, writeFile: writeFile}
}

func (s *Service) PostComment(docID string, a domain.Anchor, author string) (domain.Comment, error) {
	doc, err := s.store.GetDocument(docID)
	if err != nil {
		return domain.Comment{}, err
	}
	return s.store.CreateComment(domain.Comment{
		DocID: docID, AgainstVersionID: doc.CurrentVersionID, Anchor: a,
		AuthorIdentity: author, Owner: author, Status: domain.CommentOpen,
	})
}

func (s *Service) Claim(commentIDs []string, agent string) (string, error) {
	token := domain.NewID()
	for _, id := range commentIDs {
		if err := s.store.UpdateCommentStatus(id, domain.CommentClaimed, token); err != nil {
			return "", err
		}
	}
	return token, nil
}

func (s *Service) requireToken(commentID, token string) (domain.Comment, error) {
	c, err := s.store.GetComment(commentID)
	if err != nil {
		return domain.Comment{}, err
	}
	if c.ClaimToken == "" || c.ClaimToken != token {
		return domain.Comment{}, errors.New("invalid or missing claim token")
	}
	return c, nil
}

func (s *Service) Propose(commentID, token, content, agent string) (domain.Suggestion, error) {
	c, err := s.requireToken(commentID, token)
	if err != nil {
		return domain.Suggestion{}, err
	}
	sg, err := s.store.CreateSuggestion(domain.Suggestion{
		CommentID: commentID, AgainstVersionID: c.AgainstVersionID,
		ProposedContent: content, State: domain.SuggestionProposed, CreatedBy: agent,
	})
	if err != nil {
		return domain.Suggestion{}, err
	}
	_ = s.store.UpdateCommentStatus(commentID, domain.CommentAddressed, c.ClaimToken)
	return sg, nil
}

func (s *Service) Reply(commentID, token, body, agent string) error {
	c, err := s.requireToken(commentID, token)
	if err != nil {
		return err
	}
	if _, err := s.store.AddThreadMessage(domain.ThreadMessage{
		CommentID: commentID, AuthorIdentity: agent, Body: body,
	}); err != nil {
		return err
	}
	return s.store.UpdateCommentStatus(commentID, domain.CommentReplied, c.ClaimToken)
}

func (s *Service) Accept(commentID string) (domain.Version, error) {
	c, err := s.store.GetComment(commentID)
	if err != nil {
		return domain.Version{}, err
	}
	sg, ok, err := s.store.GetSuggestionByComment(commentID)
	if err != nil {
		return domain.Version{}, err
	}
	if !ok {
		return domain.Version{}, errors.New("no suggestion to accept")
	}
	doc, err := s.store.GetDocument(c.DocID)
	if err != nil {
		return domain.Version{}, err
	}
	oldVer, err := s.store.GetVersion(doc.CurrentVersionID)
	if err != nil {
		return domain.Version{}, err
	}

	newVer, err := s.store.AddVersion(doc.ID, sg.ProposedContent, sg.CreatedBy)
	if err != nil {
		return domain.Version{}, err
	}
	if err := s.writeFile(doc.Path, newVer.Content); err != nil {
		return domain.Version{}, err
	}
	_ = s.store.UpdateSuggestionState(sg.ID, domain.SuggestionAccepted)
	_ = s.store.UpdateCommentStatus(commentID, domain.CommentResolved, "")

	comments, err := s.store.ListComments(doc.ID)
	if err != nil {
		return domain.Version{}, err
	}
	for _, oc := range comments {
		if oc.ID == commentID || oc.AgainstVersionID != oldVer.ID {
			continue
		}
		if oc.Status == domain.CommentResolved || oc.Status == domain.CommentDetached {
			continue
		}
		na, ok := anchor.Remap(oldVer.Content, newVer.Content, oc.Anchor)
		if !ok {
			_ = s.store.UpdateCommentStatus(oc.ID, domain.CommentDetached, oc.ClaimToken)
			continue
		}
		_ = s.store.RebaseComment(oc.ID, newVer.ID, na, oc.Status)
	}
	return newVer, nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `CGO_ENABLED=0 go test ./internal/...`
Expected: PASS (domain, anchor, store, service).

- [ ] **Step 6: Commit**

```bash
git add internal/
git commit -s -m "feat: service loop with accept + re-anchoring (hypothesis proven)"
```

## Task 7: HTTP API for the browser UI (TDD)

**Files:**
- Create: `internal/api/api.go`, `internal/api/api_test.go`

**Interfaces:**
- Produces: `func NewAPI(svc *service.Service, st *store.Store) http.Handler`. Routes (all JSON):
  - `GET  /api/docs` → `[]domain.Document`
  - `GET  /api/docs/{id}` → `{ "document": Document, "content": string, "comments": []Comment }`
  - `POST /api/docs/{id}/comments` body `{ "start": int, "end": int }` → `Comment` (author `"human"`)
  - `GET  /api/comments/{id}/suggestion` → `Suggestion` or `404`
  - `POST /api/comments/{id}/accept` → `{ "version": Version }`

- [ ] **Step 1: Write the failing test**

`internal/api/api_test.go`:
```go
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rajanrx/outbox-md/internal/service"
	"github.com/rajanrx/outbox-md/internal/store"
)

func TestDocAndCommentEndpoints(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _ string) error { return nil })
	doc, _, _ := s.CreateDocument("spec.md", "Hello world", "human")
	h := NewAPI(svc, s)

	// list docs
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/docs", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "spec.md") {
		t.Fatalf("list docs: %d %s", rec.Code, rec.Body.String())
	}

	// post a comment on "world"
	rec = httptest.NewRecorder()
	body := strings.NewReader(`{"start":6,"end":11}`)
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/docs/"+doc.ID+"/comments", body))
	if rec.Code != 200 {
		t.Fatalf("post comment: %d %s", rec.Code, rec.Body.String())
	}

	// get doc → includes content + the comment
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/docs/"+doc.ID, nil))
	var got struct {
		Content  string        `json:"content"`
		Comments []json.RawMessage `json:"comments"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Content != "Hello world" || len(got.Comments) != 1 {
		t.Fatalf("get doc: content=%q comments=%d", got.Content, len(got.Comments))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/...`
Expected: FAIL — `undefined: NewAPI`.

- [ ] **Step 3: Write the implementation**

`internal/api/api.go`:
```go
package api

import (
	"encoding/json"
	"net/http"

	"github.com/rajanrx/outbox-md/internal/domain"
	"github.com/rajanrx/outbox-md/internal/service"
	"github.com/rajanrx/outbox-md/internal/store"
)

func NewAPI(svc *service.Service, st *store.Store) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/docs", func(w http.ResponseWriter, _ *http.Request) {
		docs, err := st.ListDocuments()
		writeJSON(w, docs, err)
	})

	mux.HandleFunc("GET /api/docs/{id}", func(w http.ResponseWriter, r *http.Request) {
		doc, err := st.GetDocument(r.PathValue("id"))
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		ver, err := st.GetVersion(doc.CurrentVersionID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		comments, err := st.ListComments(doc.ID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{
			"document": doc, "content": ver.Content, "comments": comments,
		}, nil)
	})

	mux.HandleFunc("POST /api/docs/{id}/comments", func(w http.ResponseWriter, r *http.Request) {
		var in domain.Anchor
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		c, err := svc.PostComment(r.PathValue("id"), in, "human")
		writeJSON(w, c, err)
	})

	mux.HandleFunc("GET /api/comments/{id}/suggestion", func(w http.ResponseWriter, r *http.Request) {
		sg, ok, err := st.GetSuggestionByComment(r.PathValue("id"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !ok {
			http.Error(w, "no suggestion", http.StatusNotFound)
			return
		}
		writeJSON(w, sg, nil)
	})

	mux.HandleFunc("POST /api/comments/{id}/accept", func(w http.ResponseWriter, r *http.Request) {
		v, err := svc.Accept(r.PathValue("id"))
		writeJSON(w, map[string]any{"version": v}, err)
	})

	return mux
}

func writeJSON(w http.ResponseWriter, v any, err error) {
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `CGO_ENABLED=0 go test ./internal/api/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/
git commit -s -m "feat: http json api for the browser ui"
```

## Task 8: MCP server — 5 ops (TDD on handlers; SDK wiring isolated)

**R4 note:** the official Go MCP SDK is newer than the TS one. To keep the risky SDK surface isolated, put all logic in plain handler functions (unit-tested against the service), and write a **thin adapter** that registers them as MCP tools. **Before writing the adapter, confirm the current SDK API** (`go doc github.com/modelcontextprotocol/go-sdk/mcp` or context7). If the SDK is unworkable, the loop still functions over HTTP; record the finding against §19 R4.

**Files:**
- Create: `internal/mcp/handlers.go`, `internal/mcp/handlers_test.go`, `internal/mcp/server.go`
- Modify: `go.mod` (adds the MCP SDK)

**Interfaces:**
- Produces (pure, SDK-independent):
  - `type Handlers struct { Svc *service.Service; St *store.Store }`
  - `func (h *Handlers) ReadDoc(docID string) (map[string]any, error)`
  - `func (h *Handlers) ListOpenComments() ([]domain.Comment, error)`
  - `func (h *Handlers) ClaimComment(ids []string, agent string) (string, error)`
  - `func (h *Handlers) ProposeSuggestion(commentID, token, content, agent string) (domain.Suggestion, error)`
  - `func (h *Handlers) ReplyInThread(commentID, token, body, agent string) error`
  - `func NewServer(h *Handlers) (*mcpserver, error)` — the SDK adapter (type name per SDK).

- [ ] **Step 1: Write the failing test (handlers only)**

`internal/mcp/handlers_test.go`:
```go
package mcp

import (
	"testing"

	"github.com/rajanrx/outbox-md/internal/domain"
	"github.com/rajanrx/outbox-md/internal/service"
	"github.com/rajanrx/outbox-md/internal/store"
)

func TestHandlersDriveTheLoop(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _ string) error { return nil })
	doc, _, _ := s.CreateDocument("spec.md", "Hello world", "human")
	c, _ := svc.PostComment(doc.ID, domain.Anchor{Start: 6, End: 11}, "human")
	h := &Handlers{Svc: svc, St: s}

	open, _ := h.ListOpenComments()
	if len(open) != 1 {
		t.Fatalf("open = %d, want 1", len(open))
	}
	tok, err := h.ClaimComment([]string{c.ID}, "agent")
	if err != nil || tok == "" {
		t.Fatalf("claim: tok=%q err=%v", tok, err)
	}
	sg, err := h.ProposeSuggestion(c.ID, tok, "Hello there", "agent")
	if err != nil || sg.ProposedContent != "Hello there" {
		t.Fatalf("propose: %+v %v", sg, err)
	}
	rd, _ := h.ReadDoc(doc.ID)
	if rd["content"] != "Hello world" {
		t.Fatalf("read_doc content = %v", rd["content"])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcp/...`
Expected: FAIL — `undefined: Handlers`.

- [ ] **Step 3: Write the handlers**

`internal/mcp/handlers.go`:
```go
package mcp

import (
	"github.com/rajanrx/outbox-md/internal/domain"
	"github.com/rajanrx/outbox-md/internal/service"
	"github.com/rajanrx/outbox-md/internal/store"
)

type Handlers struct {
	Svc *service.Service
	St  *store.Store
}

func (h *Handlers) ReadDoc(docID string) (map[string]any, error) {
	doc, err := h.St.GetDocument(docID)
	if err != nil {
		return nil, err
	}
	ver, err := h.St.GetVersion(doc.CurrentVersionID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"document": doc, "content": ver.Content}, nil
}

func (h *Handlers) ListOpenComments() ([]domain.Comment, error) {
	return h.St.ListOpenComments()
}

func (h *Handlers) ClaimComment(ids []string, agent string) (string, error) {
	return h.Svc.Claim(ids, agent)
}

func (h *Handlers) ProposeSuggestion(commentID, token, content, agent string) (domain.Suggestion, error) {
	return h.Svc.Propose(commentID, token, content, agent)
}

func (h *Handlers) ReplyInThread(commentID, token, body, agent string) error {
	return h.Svc.Reply(commentID, token, body, agent)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/mcp/ -run TestHandlersDriveTheLoop`
Expected: PASS.

- [ ] **Step 5: Add the SDK adapter**

```bash
go get github.com/modelcontextprotocol/go-sdk/mcp
```

Write `internal/mcp/server.go` to register each handler as an MCP tool over the SDK, exposing an HTTP/SSE handler the main server can mount at `/mcp`. **Verify the exact SDK types/signatures first** (`go doc .../mcp` or context7). Sketch (adapt names to the SDK):
```go
package mcp

import "github.com/modelcontextprotocol/go-sdk/mcp"

// NewServer registers the five tools (read_doc, list_open_comments,
// claim_comment, propose_suggestion, reply_in_thread) backed by h,
// and returns the SDK server. Tool input schemas mirror the handler
// arguments; tool outputs are the handler return values as JSON.
func NewServer(h *Handlers) (*mcp.Server, error) {
	s := mcp.NewServer(/* name, version, options per SDK */)
	// mcp.AddTool(s, "read_doc", ..., func(args struct{ DocID string }) (any, error) {
	//     return h.ReadDoc(args.DocID)
	// })
	// ... repeat for the other four tools ...
	return s, nil
}
```
Acceptance: `go build ./...` compiles, and `go vet ./...` passes. Manual smoke test (an MCP client lists 5 tools) happens in Task 13.

- [ ] **Step 6: Commit**

```bash
git add internal/mcp/ go.mod go.sum
git commit -s -m "feat: mcp handlers (tested) + sdk tool adapter"
```

---

## Task 9: Frontend — API client, selection util (TDD), and UI

The skeleton editor is a **read-only `<textarea>`** (its `selectionStart/End` give offsets directly). CodeMirror/TipTap and live rendering are v1-complete — do not add them here.

**Files:**
- Create: `web/src/api.ts`, `web/src/lib/selection.ts`, `web/src/lib/selection.test.ts`, `web/src/components/Editor.tsx`, `web/src/components/Outbox.tsx`, `web/src/components/Suggestion.tsx`
- Modify: `web/src/App.tsx`

**Interfaces:**
- Produces: `selectionToAnchor(text, from, to) → {start, end}` (rune offsets), plus typed API client functions.

- [ ] **Step 1: Write the failing test for the selection util**

`web/src/lib/selection.test.ts`:
```ts
import { expect, test } from "vitest";
import { selectionToAnchor } from "./selection";

test("ascii selection maps 1:1", () => {
  expect(selectionToAnchor("Hello world", 6, 11)).toEqual({ start: 6, end: 11 });
});

test("astral chars count as one rune", () => {
  // "😀x": the emoji is 2 UTF-16 units; selecting "x" is JS [2,3) → runes [1,2)
  expect(selectionToAnchor("😀x", 2, 3)).toEqual({ start: 1, end: 2 });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npm --prefix web test`
Expected: FAIL — cannot find `./selection`.

- [ ] **Step 3: Write the util**

`web/src/lib/selection.ts`:
```ts
export type Anchor = { start: number; end: number };

function toRuneOffset(text: string, jsIndex: number): number {
  return [...text.slice(0, jsIndex)].length;
}

export function selectionToAnchor(text: string, from: number, to: number): Anchor {
  return { start: toRuneOffset(text, from), end: toRuneOffset(text, to) };
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npm --prefix web test`
Expected: PASS.

- [ ] **Step 5: Write the API client**

`web/src/api.ts`:
```ts
import type { Anchor } from "./lib/selection";
export type { Anchor };

export type Comment = {
  id: string; anchor: Anchor; status: string; authorIdentity: string;
};
export type DocView = {
  document: { id: string; path: string };
  content: string;
  comments: Comment[];
};
export type Suggestion = { id: string; proposedContent: string; state: string };

export async function listDocs(): Promise<{ id: string; path: string }[]> {
  return (await fetch("/api/docs")).json();
}
export async function getDoc(id: string): Promise<DocView> {
  return (await fetch(`/api/docs/${id}`)).json();
}
export async function postComment(id: string, a: Anchor): Promise<Comment> {
  return (await fetch(`/api/docs/${id}/comments`, { method: "POST", body: JSON.stringify(a) })).json();
}
export async function getSuggestion(commentId: string): Promise<Suggestion | null> {
  const r = await fetch(`/api/comments/${commentId}/suggestion`);
  return r.ok ? r.json() : null;
}
export async function accept(commentId: string): Promise<unknown> {
  return (await fetch(`/api/comments/${commentId}/accept`, { method: "POST" })).json();
}
```

- [ ] **Step 6: Write the components**

`web/src/components/Editor.tsx`:
```tsx
import { useState } from "react";
import { selectionToAnchor, type Anchor } from "../lib/selection";
import { postComment } from "../api";

export function Editor({ docId, content, onChange }: {
  docId: string; content: string; onChange: () => void;
}) {
  const [sel, setSel] = useState<Anchor | null>(null);
  return (
    <div>
      <textarea
        readOnly
        value={content}
        style={{ width: "100%", height: 400, fontFamily: "monospace" }}
        onSelect={(e) => {
          const t = e.currentTarget;
          setSel(selectionToAnchor(content, t.selectionStart, t.selectionEnd));
        }}
      />
      <button
        disabled={!sel || sel.start === sel.end}
        onClick={async () => {
          if (sel) { await postComment(docId, sel); setSel(null); onChange(); }
        }}
      >Comment on selection</button>
    </div>
  );
}
```

`web/src/components/Outbox.tsx`:
```tsx
import type { Comment } from "../api";

export function Outbox({ comments, onSelect }: {
  comments: Comment[]; onSelect: (c: Comment) => void;
}) {
  return (
    <div>
      <h3>Outbox ({comments.length})</h3>
      <ul>
        {comments.map((c) => (
          <li key={c.id}>
            <button onClick={() => onSelect(c)}>
              [{c.status}] {c.anchor.start}–{c.anchor.end} · {c.authorIdentity}
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}
```

`web/src/components/Suggestion.tsx`:
```tsx
import { useEffect, useState } from "react";
import { getSuggestion, accept, type Suggestion } from "../api";

export function SuggestionView({ commentId, onAccepted }: {
  commentId: string; onAccepted: () => void;
}) {
  const [sg, setSg] = useState<Suggestion | null>(null);
  useEffect(() => { getSuggestion(commentId).then(setSg); }, [commentId]);
  if (!sg) return <p>No suggestion yet for this comment.</p>;
  return (
    <div>
      <h4>Proposed content</h4>
      <pre style={{ whiteSpace: "pre-wrap", background: "#f4f4f4", padding: 8 }}>
        {sg.proposedContent}
      </pre>
      <button onClick={async () => { await accept(commentId); onAccepted(); }}>Accept</button>
    </div>
  );
}
```

`web/src/App.tsx`:
```tsx
import { useEffect, useState } from "react";
import { listDocs, getDoc, type DocView, type Comment } from "./api";
import { Editor } from "./components/Editor";
import { Outbox } from "./components/Outbox";
import { SuggestionView } from "./components/Suggestion";

export default function App() {
  const [docId, setDocId] = useState("");
  const [view, setView] = useState<DocView | null>(null);
  const [sel, setSel] = useState<Comment | null>(null);

  const refresh = async (id: string) => setView(await getDoc(id));

  useEffect(() => { listDocs().then((ds) => { if (ds?.length) setDocId(ds[0].id); }); }, []);
  useEffect(() => {
    if (!docId) return;
    refresh(docId);
    const t = setInterval(() => refresh(docId), 2000);
    return () => clearInterval(t);
  }, [docId]);

  if (!view) return <div style={{ padding: 24 }}>No documents. Mount a folder of .md files.</div>;
  return (
    <div style={{ display: "flex", gap: 24, padding: 24, fontFamily: "system-ui" }}>
      <div style={{ flex: 2 }}>
        <h2>{view.document.path}</h2>
        <Editor docId={docId} content={view.content} onChange={() => refresh(docId)} />
      </div>
      <div style={{ flex: 1 }}>
        <Outbox comments={view.comments} onSelect={setSel} />
        {sel && <SuggestionView commentId={sel.id} onAccepted={() => { setSel(null); refresh(docId); }} />}
      </div>
    </div>
  );
}
```

- [ ] **Step 7: Verify build + tests**

Run: `npm --prefix web run build && npm --prefix web test`
Expected: build succeeds, selection tests pass.

- [ ] **Step 8: Commit**

```bash
git add web/
git commit -s -m "feat: skeleton frontend (textarea editor, outbox, suggestion, accept)"
```

## Task 10: Main wiring — embed SPA, import folder, mount API + MCP

**Files:**
- Create: `web/embed.go`, `web/dist/index.html` (placeholder so `go build` works without npm)
- Modify: `cmd/outbox-md/main.go`, `internal/store/documents.go` (add `GetDocumentByPath`)

**Interfaces:**
- Modify store: `func (s *Store) GetDocumentByPath(path string) (domain.Document, bool, error)`

- [ ] **Step 1: Add `GetDocumentByPath` to the store**

Append to `internal/store/documents.go`:
```go
import "database/sql" // add to existing import block if not present
import "errors"       // add to existing import block if not present

func (s *Store) GetDocumentByPath(path string) (domain.Document, bool, error) {
	var d domain.Document
	var cur *string
	err := s.DB.QueryRow(`SELECT id, path, current_version_id FROM documents WHERE path=?`, path).
		Scan(&d.ID, &d.Path, &cur)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Document{}, false, nil
	}
	if cur != nil {
		d.CurrentVersionID = *cur
	}
	return d, err == nil, err
}
```

- [ ] **Step 2: Create the embed package**

`web/embed.go`:
```go
package web

import "embed"

//go:embed all:dist
var Dist embed.FS
```

`web/dist/index.html` (placeholder; the real one is produced by `npm run build`):
```html
<!doctype html><title>outbox-md</title><p>build the frontend</p>
```

- [ ] **Step 3: Rewrite `cmd/outbox-md/main.go`**

```go
package main

import (
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/rajanrx/outbox-md/internal/api"
	"github.com/rajanrx/outbox-md/internal/service"
	"github.com/rajanrx/outbox-md/internal/store"
	"github.com/rajanrx/outbox-md/web"
	// mcpsrv "github.com/rajanrx/outbox-md/internal/mcp"
)

func newMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func importMarkdown(st *store.Store, dir string) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".outbox" || (strings.HasPrefix(d.Name(), ".") && p != dir) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		if _, ok, _ := st.GetDocumentByPath(rel); ok {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		_, _, err = st.CreateDocument(rel, string(b), "import")
		return err
	})
}

func main() {
	dir := getenv("OUTBOX_DIR", "/data")
	addr := getenv("OUTBOX_ADDR", ":8080")

	dbDir := filepath.Join(dir, ".outbox")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		log.Fatal(err)
	}
	st, err := store.Open("file:" + filepath.Join(dbDir, "outbox.db"))
	if err != nil {
		log.Fatal(err)
	}
	svc := service.New(st, func(path, content string) error {
		return os.WriteFile(filepath.Join(dir, path), []byte(content), 0o644)
	})
	if err := importMarkdown(st, dir); err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("/api/", api.NewAPI(svc, st))

	// MCP at /mcp — wire per the verified SDK (Task 8). Example:
	//   srv, _ := mcpsrv.NewServer(&mcpsrv.Handlers{Svc: svc, St: st})
	//   mux.Handle("/mcp", srv.HTTPSSEHandler())   // adapt to SDK's HTTP/SSE handler
	// Until wired, agents connect over stdio via a separate entrypoint.

	sub, _ := fs.Sub(web.Dist, "dist")
	mux.Handle("/", http.FileServer(http.FS(sub)))

	log.Printf("outbox-md on %s serving %s", addr, dir)
	log.Fatal(http.ListenAndServe(addr, mux))
}
```

- [ ] **Step 4: Verify build + existing tests**

Run: `CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test ./...`
Expected: builds; all Go tests pass (`TestHealthz` still green via `newMux`).

- [ ] **Step 5: Commit**

```bash
git add cmd/ internal/store/documents.go web/embed.go web/dist/index.html
git commit -s -m "feat: wire server — embed spa, import md folder, mount api"
```

## Task 11: Dev "simulate agent" endpoints + end-to-end verification

To exercise the full loop without a live LLM, expose the agent ops over HTTP behind a dev flag, then run the loop manually and **verify the hypothesis on a real running container**.

**Files:**
- Modify: `internal/api/api.go` (add dev routes when `OUTBOX_DEV=1`), `internal/api/api_test.go` (test claim+propose), `README.md` (quickstart), `CHANGELOG.md`

- [ ] **Step 1: Write the failing test**

Add to `internal/api/api_test.go`:
```go
func TestDevClaimAndPropose(t *testing.T) {
	t.Setenv("OUTBOX_DEV", "1")
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _ string) error { return nil })
	doc, _, _ := s.CreateDocument("spec.md", "Hello world", "human")
	c, _ := svc.PostComment(doc.ID, domain.Anchor{Start: 0, End: 5}, "human")
	h := NewAPI(svc, s)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
		"/api/dev/claim", strings.NewReader(`{"commentIds":["`+c.ID+`"]}`)))
	if rec.Code != 200 {
		t.Fatalf("claim: %d %s", rec.Code, rec.Body.String())
	}
	var cl struct{ Token string `json:"token"` }
	_ = json.Unmarshal(rec.Body.Bytes(), &cl)

	rec = httptest.NewRecorder()
	body := `{"commentId":"` + c.ID + `","token":"` + cl.Token + `","content":"Say Hello world"}`
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/dev/propose", strings.NewReader(body)))
	if rec.Code != 200 {
		t.Fatalf("propose: %d %s", rec.Code, rec.Body.String())
	}
}
```
Add imports as needed: `"github.com/rajanrx/outbox-md/internal/domain"`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/ -run TestDevClaimAndPropose`
Expected: FAIL — 404 (routes not registered).

- [ ] **Step 3: Add the dev routes**

In `internal/api/api.go`, inside `NewAPI`, before `return mux`:
```go
	if os.Getenv("OUTBOX_DEV") == "1" {
		mux.HandleFunc("POST /api/dev/claim", func(w http.ResponseWriter, r *http.Request) {
			var in struct {
				CommentIDs []string `json:"commentIds"`
			}
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			tok, err := svc.Claim(in.CommentIDs, "dev-agent")
			writeJSON(w, map[string]any{"token": tok}, err)
		})
		mux.HandleFunc("POST /api/dev/propose", func(w http.ResponseWriter, r *http.Request) {
			var in struct {
				CommentID string `json:"commentId"`
				Token     string `json:"token"`
				Content   string `json:"content"`
			}
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			sg, err := svc.Propose(in.CommentID, in.Token, in.Content, "dev-agent")
			writeJSON(w, sg, err)
		})
	}
```
Add `"os"` to the imports.

- [ ] **Step 4: Run test to verify it passes**

Run: `CGO_ENABLED=0 go test ./internal/api/...`
Expected: PASS.

- [ ] **Step 5: Write the README quickstart + changelog entry**

`README.md` quickstart:
````markdown
## Quickstart (walking skeleton)

```bash
docker build -t outbox-md:dev .
mkdir -p sample && printf "# Spec\n\nHello world\n" > sample/spec.md
docker run --rm -p 8080:8080 -e OUTBOX_DEV=1 -v "$PWD/sample:/data" outbox-md:dev
# open http://localhost:8080
```
````
Add to `CHANGELOG.md` under `## [Unreleased]`: `- Walking skeleton: annotate → outbox → agent proposes → accept re-anchors and rewrites the file.`

- [ ] **Step 6: Manual end-to-end verification (the hypothesis, live)**

Run the container as in the quickstart, then:
```bash
# 1. In the browser: select "world" in spec.md, click "Comment on selection".
# 2. List the comment id:
curl -s localhost:8080/api/docs | jq -r '.[0].id' | tee /tmp/doc
DOC=$(cat /tmp/doc)
C1=$(curl -s localhost:8080/api/docs/$DOC | jq -r '.comments[0].id')
# 3. Add a SECOND comment on "Hello" via the browser, then capture it:
C2=$(curl -s localhost:8080/api/docs/$DOC | jq -r '.comments[] | select(.anchor.start==0) | .id')
# 4. Simulate the agent: claim C2 and propose inserting text before it.
TOK=$(curl -s -XPOST localhost:8080/api/dev/claim -d "{\"commentIds\":[\"$C2\"]}" | jq -r .token)
curl -s -XPOST localhost:8080/api/dev/propose \
  -d "{\"commentId\":\"$C2\",\"token\":\"$TOK\",\"content\":\"Say Hello world\"}" >/dev/null
# 5. In the browser: click C2 in the outbox → Accept.
# 6. VERIFY:
cat sample/spec.md            # → contains "Say Hello world"  (file rewritten)
curl -s localhost:8080/api/docs/$DOC | jq '.comments[] | {start:.anchor.start, status}'
#   → the "world" comment now reports anchor.start == 10 (re-anchored), status "open"
```

Acceptance: the on-disk file equals the accepted content, **and** the untouched comment's anchor followed its text (start moved 6 → 10). If both hold, **the hypothesis is proven** and the skeleton is done.

- [ ] **Step 7: Commit**

```bash
git add internal/api/ README.md CHANGELOG.md
git commit -s -m "feat: dev simulate-agent endpoints + e2e verification of the loop"
```

---

## Done criteria for this plan

- All Go tests pass with `CGO_ENABLED=0 go test ./...`; `npm --prefix web test` passes; CI green.
- `docker build` produces a runnable image; the manual e2e in Task 11 confirms file-rewrite + re-anchoring on a live container.
- **The hypothesis is proven** (Task 6 unit + Task 11 live). If anchoring proved unreliable, the spike has done its job — record findings against §19 R1/R2 and revisit the anchoring model before v1-complete.
- MCP: handlers unit-tested; SDK adapter compiles. Full MCP transport wiring may carry into early v1-complete if §19 R4 (SDK maturity) demands — the HTTP loop already proves the mechanics.
