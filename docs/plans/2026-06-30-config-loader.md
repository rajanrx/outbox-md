# Config Loader (`outbox.yaml`) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Load a committed `outbox.yaml` from the specs folder and enforce its settings server-side (`agent.batch_size`, `approval.post_approval_comments`), exposed read-only at `GET /api/config`.

**Architecture:** A new `internal/config` package loads `outbox.yaml` over built-in defaults (forgiving — missing/malformed → defaults). The `Service` holds the effective config (defaulted in `New`, replaced by `SetConfig` from `main` at startup) and enforces it in `Claim` and `PostComment`. The config is served at `GET /api/config`.

**Tech Stack:** Go 1.25 (`database/sql`, `net/http`), `gopkg.in/yaml.v3` (new dependency).

**Spec:** `docs/specs/2026-06-30-config-loader-design.md`

## Global Constraints

- Commit identity is `rajan <rajanrauniyar@gmail.com>`. NEVER add a `Co-Authored-By: Claude` trailer. Use `git -c user.name='rajan' -c user.email='rajanrauniyar@gmail.com' commit -m "..."`.
- Config is **enforced server-side** — the substrate is authoritative; an agent cannot bypass it.
- **Defaults reproduce today's behaviour:** `batch_size: 5`, `post_approval_comments: true`. Adding config must change nothing until a user writes an `outbox.yaml`, and all existing tests must stay green.
- Missing/malformed `outbox.yaml` → defaults + a single log line; startup never fails on config. `batch_size < 1` → default.
- Over-batch claim is **rejected** (not truncated).
- One new dependency only: `gopkg.in/yaml.v3`.
- Go tests run in the dev backend container from repo root `/Users/rajanrauniyar/Development/outbox-md`: `docker compose -f docker-compose.dev.yml exec -T backend go test ./...` (single pkg: `... go test ./internal/config/ -run TestName -v`).

---

### Task 1: `internal/config` package + `outbox.yaml` loader

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`
- Modify: `go.mod`, `go.sum` (add `gopkg.in/yaml.v3` via `go get`)

**Interfaces:**
- Produces: `config.Config{Agent config.AgentConfig; Approval config.ApprovalConfig}` where `AgentConfig{BatchSize int}` and `ApprovalConfig{PostApprovalComments bool}`; `config.Defaults() config.Config`; `config.Load(dir string) config.Config`. JSON tags: `agent.batchSize`, `approval.postApprovalComments`. YAML keys: `agent.batch_size`, `approval.post_approval_comments`.

- [ ] **Step 1: Add the YAML dependency**

Run (in the dev container, repo root): `docker compose -f docker-compose.dev.yml exec -T backend go get gopkg.in/yaml.v3`
Expected: `go.mod`/`go.sum` updated with `gopkg.in/yaml.v3`.

- [ ] **Step 2: Write the failing test** — create `internal/config/config_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "outbox.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadDefaultsWhenAbsent(t *testing.T) {
	cfg := Load(t.TempDir())
	if cfg.Agent.BatchSize != 5 || !cfg.Approval.PostApprovalComments {
		t.Fatalf("defaults = %+v, want {batch 5, postApproval true}", cfg)
	}
}

func TestLoadOverridesBatchSizeKeepsOtherDefaults(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "agent:\n  batch_size: 3\n")
	cfg := Load(dir)
	if cfg.Agent.BatchSize != 3 {
		t.Errorf("batch_size = %d, want 3", cfg.Agent.BatchSize)
	}
	if !cfg.Approval.PostApprovalComments {
		t.Error("post_approval_comments should stay default true when omitted")
	}
}

func TestLoadDisablePostApproval(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "approval:\n  post_approval_comments: false\n")
	if Load(dir).Approval.PostApprovalComments {
		t.Error("post_approval_comments = true, want false")
	}
}

func TestLoadMalformedFallsBackToDefaults(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "agent: [this is not valid yaml")
	if Load(dir).Agent.BatchSize != 5 {
		t.Error("malformed file should fall back to default batch_size 5")
	}
}

func TestLoadZeroBatchSizeCorrected(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "agent:\n  batch_size: 0\n")
	if Load(dir).Agent.BatchSize != 5 {
		t.Error("batch_size 0 should be corrected to default 5")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `docker compose -f docker-compose.dev.yml exec -T backend go test ./internal/config/ -v`
Expected: FAIL (compile error: package has no `Load`/`Config`).

- [ ] **Step 4: Implement the package** — create `internal/config/config.go`:

```go
package config

import (
	"log"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Agent    AgentConfig    `json:"agent"    yaml:"agent"`
	Approval ApprovalConfig `json:"approval" yaml:"approval"`
}

type AgentConfig struct {
	BatchSize int `json:"batchSize" yaml:"batch_size"`
}

type ApprovalConfig struct {
	PostApprovalComments bool `json:"postApprovalComments" yaml:"post_approval_comments"`
}

// Defaults is the built-in configuration used when no outbox.yaml is present,
// and the floor every loaded config layers over.
func Defaults() Config {
	return Config{
		Agent:    AgentConfig{BatchSize: 5},
		Approval: ApprovalConfig{PostApprovalComments: true},
	}
}

// Load reads outbox.yaml from the folder root, layered over Defaults(). A
// missing file yields the defaults; a malformed file logs and falls back to the
// defaults (startup never fails on config). batch_size below 1 is corrected.
func Load(dir string) Config {
	cfg := Defaults()
	data, err := os.ReadFile(filepath.Join(dir, "outbox.yaml"))
	if err != nil {
		return cfg // not present / unreadable → defaults
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		log.Printf("outbox.yaml: invalid, using defaults: %v", err)
		return Defaults()
	}
	if cfg.Agent.BatchSize < 1 {
		cfg.Agent.BatchSize = Defaults().Agent.BatchSize
	}
	return cfg
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `docker compose -f docker-compose.dev.yml exec -T backend go test ./internal/config/ -v`
Expected: PASS (all 5).

- [ ] **Step 6: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go go.mod go.sum
git commit -m "config: outbox.yaml loader with defaults and validation"
```

---

### Task 2: Service holds + enforces the config

**Files:**
- Modify: `internal/service/service.go`
- Test: `internal/service/service_test.go`

**Interfaces:**
- Consumes: `config.Config`, `config.Defaults()` (Task 1).
- Produces: `Service` gains `cfg config.Config` (defaulted in `New`); `(*Service).SetConfig(config.Config)`; `(*Service).Config() config.Config`. `Claim` rejects an over-`batch_size` request; `PostComment` rejects a comment on an approved/amending doc when `post_approval_comments` is false.

- [ ] **Step 1: Write the failing test** — append to `internal/service/service_test.go` (package `service`; add `"github.com/rajanrx/outbox-md/internal/config"` to the imports):

```go
func TestClaimRejectsOverBatchSize(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := New(s, func(_, _ string) error { return nil })
	svc.SetConfig(config.Config{Agent: config.AgentConfig{BatchSize: 2}, Approval: config.ApprovalConfig{PostApprovalComments: true}})

	doc, _, _ := s.CreateDocument("a.md", "hello world", "human")
	c1, _ := svc.PostComment(doc.ID, domain.Anchor{Start: 0, End: 1}, "human")
	c2, _ := svc.PostComment(doc.ID, domain.Anchor{Start: 1, End: 2}, "human")
	c3, _ := svc.PostComment(doc.ID, domain.Anchor{Start: 2, End: 3}, "human")

	if _, err := svc.Claim([]string{c1.ID, c2.ID, c3.ID}, "agent"); err == nil {
		t.Fatal("claiming 3 with batch_size 2 should be rejected")
	}
	if got, _ := s.GetComment(c1.ID); got.Status != domain.CommentOpen {
		t.Errorf("c1 status = %s, want open (over-batch claim must claim nothing)", got.Status)
	}
	if _, err := svc.Claim([]string{c1.ID, c2.ID}, "agent"); err != nil {
		t.Fatalf("within-cap claim failed: %v", err)
	}
}

func TestPostCommentBlockedOnApprovedWhenDisabled(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := New(s, func(_, _ string) error { return nil })
	svc.SetConfig(config.Config{Agent: config.AgentConfig{BatchSize: 5}, Approval: config.ApprovalConfig{PostApprovalComments: false}})

	doc, _, _ := s.CreateDocument("a.md", "hello", "human")
	if _, err := svc.Approve(doc.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PostComment(doc.ID, domain.Anchor{Start: 0, End: 1}, "human"); err == nil {
		t.Fatal("post-approval comment should be rejected when disabled")
	}
	svc.SetConfig(config.Config{Agent: config.AgentConfig{BatchSize: 5}, Approval: config.ApprovalConfig{PostApprovalComments: true}})
	if _, err := svc.PostComment(doc.ID, domain.Anchor{Start: 0, End: 1}, "human"); err != nil {
		t.Fatalf("post-approval comment should be allowed when enabled: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `docker compose -f docker-compose.dev.yml exec -T backend go test ./internal/service/ -run 'TestClaimRejectsOverBatchSize|TestPostCommentBlockedOnApprovedWhenDisabled' -v`
Expected: FAIL (compile error: `SetConfig` undefined; and the behaviour isn't enforced).

- [ ] **Step 3: Add the config field, setter, and getter** — in `internal/service/service.go`, add `"fmt"` and `"github.com/rajanrx/outbox-md/internal/config"` to imports, and update the struct + constructor:

```go
type Service struct {
	store     *store.Store
	writeFile func(path, content string) error
	cfg       config.Config
}

func New(st *store.Store, writeFile func(path, content string) error) *Service {
	return &Service{store: st, writeFile: writeFile, cfg: config.Defaults()}
}

// SetConfig replaces the effective configuration (called once at startup with
// the loaded outbox.yaml).
func (s *Service) SetConfig(cfg config.Config) { s.cfg = cfg }

// Config returns the effective configuration (read-only view for the API).
func (s *Service) Config() config.Config { return s.cfg }
```

- [ ] **Step 4: Enforce `batch_size` in `Claim`** — in `internal/service/service.go`, add the cap at the top of `Claim`:

```go
func (s *Service) Claim(commentIDs []string, agent string) (string, error) {
	if len(commentIDs) > s.cfg.Agent.BatchSize {
		return "", fmt.Errorf("batch size exceeded: at most %d comments per claim", s.cfg.Agent.BatchSize)
	}
	token := domain.NewID()
	for _, id := range commentIDs {
		if err := s.store.UpdateCommentStatus(id, domain.CommentClaimed, token); err != nil {
			return "", err
		}
	}
	return token, nil
}
```

- [ ] **Step 5: Enforce `post_approval_comments` in `PostComment`** — in `internal/service/service.go`, gate the governed case:

```go
func (s *Service) PostComment(docID string, a domain.Anchor, author string) (domain.Comment, error) {
	doc, err := s.store.GetDocument(docID)
	if err != nil {
		return domain.Comment{}, err
	}
	governed := doc.Status == domain.DocApproved || doc.Status == domain.DocAmending
	if governed && !s.cfg.Approval.PostApprovalComments {
		return domain.Comment{}, errors.New("comments are disabled on approved documents")
	}
	return s.store.CreateComment(domain.Comment{
		DocID: docID, AgainstVersionID: doc.CurrentVersionID, Anchor: a,
		AuthorIdentity: author, Owner: author, Status: domain.CommentOpen,
		PostApproval: governed,
	})
}
```

> `errors` is already imported in `service.go`. Keep the rest of `PostComment` semantics identical (`PostApproval = governed`).

- [ ] **Step 6: Run the new tests, then the whole module**

Run: `docker compose -f docker-compose.dev.yml exec -T backend go test ./internal/service/ -v`
Expected: PASS (new tests + all existing — defaults give batch_size 5 and post_approval true, so existing claims (≤2) and governance comment-on-approved tests stay green).

Run: `docker compose -f docker-compose.dev.yml exec -T backend go test ./...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/service/service.go internal/service/service_test.go
git commit -m "config: enforce batch_size on claim and post_approval_comments on commenting"
```

---

### Task 3: `GET /api/config` + startup wiring

**Files:**
- Modify: `internal/api/api.go`
- Modify: `cmd/outbox-md/main.go`
- Test: `internal/api/api_test.go`

**Interfaces:**
- Consumes: `(*Service).Config()` and `(*Service).SetConfig(...)` (Task 2); `config.Load(dir)` (Task 1); the existing `writeJSON(w, v, err)` helper.
- Produces: `GET /api/config` → the effective `config.Config` JSON; `main` loads `outbox.yaml` at startup.

- [ ] **Step 1: Write the failing test** — append to `internal/api/api_test.go` (package `api`; add `"github.com/rajanrx/outbox-md/internal/config"`, and `"encoding/json"` / `"net/http/httptest"` if not present):

```go
func TestConfigEndpoint(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _ string) error { return nil })
	h := NewAPI(svc, s)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/api/config", nil))
	if rr.Code != 200 {
		t.Fatalf("status = %d", rr.Code)
	}
	var got struct {
		Agent struct {
			BatchSize int `json:"batchSize"`
		} `json:"agent"`
		Approval struct {
			PostApprovalComments bool `json:"postApprovalComments"`
		} `json:"approval"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Agent.BatchSize != 5 || !got.Approval.PostApprovalComments {
		t.Errorf("default config = %+v, want {batch 5, postApproval true}", got)
	}

	svc.SetConfig(config.Config{Agent: config.AgentConfig{BatchSize: 9}, Approval: config.ApprovalConfig{PostApprovalComments: false}})
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, httptest.NewRequest("GET", "/api/config", nil))
	_ = json.Unmarshal(rr2.Body.Bytes(), &got)
	if got.Agent.BatchSize != 9 || got.Approval.PostApprovalComments {
		t.Errorf("after SetConfig = %+v, want {batch 9, postApproval false}", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `docker compose -f docker-compose.dev.yml exec -T backend go test ./internal/api/ -run TestConfigEndpoint -v`
Expected: FAIL (404 — route not registered).

- [ ] **Step 3: Add the endpoint** — in `internal/api/api.go`, register next to the other `GET /api/...` handlers:

```go
	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, svc.Config(), nil)
	})
```

- [ ] **Step 4: Run test to verify it passes**

Run: `docker compose -f docker-compose.dev.yml exec -T backend go test ./internal/api/ -run TestConfigEndpoint -v`
Expected: PASS.

- [ ] **Step 5: Wire config load into startup** — in `cmd/outbox-md/main.go`, add `"github.com/rajanrx/outbox-md/internal/config"` to imports and load the config right after the service is constructed:

```go
	svc := service.New(st, func(path, content string) error {
		target, err := safeJoin(dir, path)
		if err != nil {
			return err
		}
		return atomicWrite(target, content)
	})
	svc.SetConfig(config.Load(dir))
```

- [ ] **Step 6: Build + full suite**

Run: `docker compose -f docker-compose.dev.yml exec -T backend go build ./... && docker compose -f docker-compose.dev.yml exec -T backend go test ./...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/api/api.go internal/api/api_test.go cmd/outbox-md/main.go
git commit -m "config: GET /api/config and load outbox.yaml at startup"
```

---

## Notes for the executor

- No shared test helpers — store/service/api tests inline `Open(":memory:")` / `store.Open(":memory:")` + `service.New(s, func(_, _ string) error { return nil })` + `NewAPI(svc, s)`. Drive config in tests with `svc.SetConfig(config.Config{...})`.
- The whole point of defaulting the config in `New` and threading it via `SetConfig` is that **every existing `service.New(...)` call site keeps working unchanged** on defaults (batch_size 5, post_approval true) — confirm the full suite stays green, since the existing concurrency/governance tests claim ≤2 comments and comment on approved docs.
- `go.mod`/`go.sum` gain `gopkg.in/yaml.v3` — the only new dependency. Run `go mod tidy` in the container if `go.sum` needs it.
- After all tasks: final whole-branch review, then push and open a PR against `main` (do not merge).
