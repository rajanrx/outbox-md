# outbox-md — Config Loader (`outbox.yaml`) (Design)

| | |
|---|---|
| **Status** | Draft — pending approval |
| **Cycle** | v1.5 · config loader |
| **Date** | 2026-06-30 |
| **Branch** | `feat/config-loader` |
| **Implements** | §6 of `2026-06-27-outbox-md-design.md` (the parts with teeth) |

## 1. Summary

Make behaviour configurable via a committed `outbox.yaml` at the specs-folder root, loaded once at startup and **enforced by the backend** — the substrate is authoritative, so an agent cannot bypass it (§4). This cycle implements only the settings that have real effect today; the rest of §6 (council, extensions, parking, front-matter, live UI toggle, dashboard) stays deferred.

## 2. Scope

**In scope:**
- Load `outbox.yaml` from the folder root at startup, layered over built-in defaults. Missing or malformed file → fall back to defaults with a clear log line; never crash on a typo.
- Enforce two settings, server-side:
  - `agent.batch_size` (default **5**) — the maximum number of comments an agent may claim in one `claim_comment`. Over-claim is **rejected** with a clear error.
  - `approval.post_approval_comments` (default **true**) — when `false`, reject *new* comments on an approved or amending document.
- Expose the effective config read-only at `GET /api/config`.

**Out of scope (deferred):** `ordering` (already strict), council, extensions, `max_attempts`/parking (no reaper yet), per-document front-matter, live UI toggle, dashboard, and an MCP `read_config` tool (the agent learns the cap from the rejection error; the tool ships with the council).

## 3. Decisions

- **Over-batch claim is rejected, not truncated** — a clear contract; the agent re-claims within the cap rather than silently getting fewer than it asked for.
- **Defaults preserve today's behaviour** — `batch_size: 5` (existing tests claim ≤2, unaffected) and `post_approval_comments: true` (commenting on approved docs still creates amendments). So adding config changes nothing until a user writes an `outbox.yaml`.
- **Threaded via a setter, not a changed constructor** — `service.New(st, writeFile)` keeps its signature and defaults the config; `main` calls `svc.SetConfig(config.Load(dir))`. This avoids touching every existing `service.New` test call site.
- **One new dependency:** `gopkg.in/yaml.v3` — standard, single, justified (the spec mandates a YAML file).

## 4. Config model

New package `internal/config`:

```go
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

func Defaults() Config // {Agent:{BatchSize:5}, Approval:{PostApprovalComments:true}}
func Load(dir string) Config
```

`Load(dir)`:
1. Start from `Defaults()`.
2. Read `dir/outbox.yaml`. Not present → return defaults.
3. `yaml.Unmarshal` over the defaults struct (so a file that sets only `agent.batch_size` leaves `post_approval_comments` at its default). On unmarshal error → log and return `Defaults()`.
4. Validate: `batch_size < 1` → reset to the default.

Example `outbox.yaml`:
```yaml
agent:
  batch_size: 5
approval:
  post_approval_comments: true
```

## 5. Service enforcement

`internal/service`:
- `Service` gains a `cfg config.Config` field; `New` initialises it to `config.Defaults()`. Add `SetConfig(config.Config)` and `Config() config.Config`.
- `Claim(commentIDs, agent)`: if `len(commentIDs) > cfg.Agent.BatchSize`, return an error `"batch size exceeded: at most N comments per claim"` before claiming anything.
- `PostComment(docID, anchor, author)`: compute `governed = status is approved|amending`; if `governed && !cfg.Approval.PostApprovalComments`, return `"comments are disabled on approved documents"`. Otherwise unchanged (still sets `PostApproval = governed`).

## 6. API & wiring

- `main.go`: `svc := service.New(st, writeFile); svc.SetConfig(config.Load(dir))` (dir is the existing `OUTBOX_DIR`/`/data` folder).
- `GET /api/config` → `writeJSON(w, svc.Config(), nil)` (read-only; no auth, consistent with the rest of the API).

## 7. Testing

- **config** (`internal/config`): `Load` returns defaults when the file is absent; a file overriding `batch_size` is applied while `post_approval_comments` stays at its default; a malformed file logs and returns defaults; `batch_size: 0` is corrected to the default.
- **service** (`internal/service`): `Claim` with more ids than `batch_size` is rejected (and claims nothing); a within-cap claim still works; `PostComment` on an approved doc is rejected when `post_approval_comments` is false and allowed when true. Use `New(...).SetConfig(custom)` to drive these; existing tests stay green on defaults.
- **API** (`internal/api`): `GET /api/config` returns the effective config (defaults, and an overridden `batch_size` after `SetConfig`).

## 8. Errors & edges

- Missing / malformed `outbox.yaml` → defaults + one log line; startup never fails on config.
- `batch_size < 1` → default (prevents a config that blocks all claims).
- The `.outbox/` state dir lives under the same folder; `outbox.yaml` sits beside it at the root and is user-authored/committed (it is read, never written, by the server).
