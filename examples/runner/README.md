# Reference webhook runner

> The client-side, **bring-your-own-agent** counterpart to the interactive MCP. It receives outbox-md webhooks and drives a **single-agent loop** — claim a comment, propose a tracked change or reply — over the same MCP tools you'd use by hand.

outbox-md has **two consumers** for the same governance events:

| | **Interactive MCP** | **Autonomous runner** (this) |
|---|---|---|
| Driver | you, in a chat session | a long-running process, on a webhook |
| Trigger | you ask the agent to "process the outbox" | a `comment.created` / `comment.replied` push |
| Loop | you watch each step | claim → propose/reply, hands-off |
| Use when | exploring, one-off review, you want to supervise every move | you want the agent to react the moment you comment, without re-prompting |

Both speak the **exact same MCP toolset** and obey the **same invariant**: the agent only **proposes** suggestions and **replies** in threads. It never resolves, accepts, or approves — those are human-only and have no MCP tool by design (see [`../../AGENTS.md`](../../AGENTS.md)).

> This is the **single-agent loop**. A multi-agent council fan-out is on the roadmap, not here.

Three functionally-equivalent implementations are provided — pick your language:

- [`go/`](go) — Go (also the canonical **`api` mode** implementation)
- [`node/`](node) — Node, stdlib-only (no `npm install` needed)
- [`python/`](python) — Python, stdlib-only

---

## Two agent modes — and the cost note

Set the backend with `RUNNER_AGENT_MODE`:

### `cli` (default) — cost-efficient, no API key

The runner spawns a **headless coding-agent CLI** that already has the outbox-md MCP configured. It reasons and writes back through MCP itself.

**This uses your existing CLI subscription — there is zero per-token API cost**, and no LLM key ever touches the runner. This is why it's the default.

The command is a template in `RUNNER_AGENT_CMD`; the literal token `{prompt}` is replaced (as a single argument, no shell) with an instruction telling the agent to process the outbox and honour the human-only invariant.

- **Claude Code (default):**
  ```
  claude -p {prompt} --allowedTools mcp__outbox-md__*
  ```
  `-p` runs headless (print mode); `--allowedTools "mcp__outbox-md__*"` pre-authorizes the outbox-md MCP tools so the run is non-interactive. (Add `--permission-mode acceptEdits` if your setup needs it.)
- **GitHub Copilot CLI (alternative):**
  ```
  RUNNER_AGENT_CMD='copilot -p {prompt} --allow-tool outbox-md'
  ```
  (Flag names vary by CLI version — consult your CLI's headless/`--help` docs; the shape is "run one prompt, allow the outbox-md MCP tools".)

The CLI must have the MCP registered first:
```bash
claude mcp add --transport http outbox-md http://localhost:8181/mcp
claude mcp list   # outbox-md ✓ connected
```

### `api` (opt-in) — bring your own key

For users who'd rather hold an API key than a CLI subscription. The runner connects to the outbox-md MCP **as a client** over Streamable-HTTP and calls an LLM directly:

> `list_open_comments` → `claim_comment` → (your LLM decides) → `propose_suggestion` **or** `reply_in_thread`.

It bills per token (`ANTHROPIC_API_KEY`).

> **api-mode asymmetry — read this.** Only the **Go** runner implements `api` mode in full (MCP client + Anthropic Messages API call, in [`go/api.go`](go/api.go)). The **Node** and **Python** runners ship a clearly-commented **stub** that throws `"api mode: implement the LLM call — see README"` and documents the exact loop to fill in. For a working autonomous setup in Node/Python, use the default `cli` mode. Go is the canonical reference if you want to port `api` mode.

---

## How it fits together (host-side setup)

```
   you (browser)            outbox-md server                 this runner            your agent
        │  comment                 │                              │                     │
        ▼                          │   POST / (webhook)           │                     │
   ┌──────────┐   event     ┌─────────────┐  X-Outbox-Event ┌───────────┐  cli: spawn   │
   │ outbox   │────────────▶│  webhook    │────────────────▶│  verify   │──────────────▶│ claude -p …
   │ UI/MCP   │             │  notifier   │  X-Outbox-Sig    │  filter   │  api: MCP +   │  (MCP tools)
   └──────────┘             └─────────────┘                  │  debounce │  LLM call     │
        ▲                                                     └───────────┘               │
        └──────────────  propose_suggestion / reply_in_thread (MCP) ◀─────────────────────┘
```

1. **Run the outbox-md server** (from the repo root):
   ```bash
   OUTBOX_DIR=path/to/your/specs docker compose up -d --build
   ```
2. **Register the MCP with your CLI** (cli mode):
   ```bash
   claude mcp add --transport http outbox-md http://localhost:8181/mcp
   ```
3. **Start the runner** on the host (see per-language quick-start below). It listens on `RUNNER_ADDR` (default `:8787`) and handles `POST /`.
4. **Point the server at the runner** so events flow — set `OUTBOX_WEBHOOK_URL` to the runner's `/`, and (recommended) a shared secret:
   ```bash
   OUTBOX_WEBHOOK_URL=http://host.docker.internal:8787/ \
   OUTBOX_WEBHOOK_SECRET=your-shared-secret \
   OUTBOX_DIR=path/to/your/specs docker compose up -d --build
   ```
   (From inside the server container, the host is `host.docker.internal`. If you run the runner in the same compose network, use its service name instead.)

Now: you comment in the browser → the server POSTs the runner → the runner triggers your agent → the agent proposes/replies over MCP → you review and accept. No polling.

### Optional: containerized runner

You can run the runner in a container too. The catch for **cli mode** is that the container needs the CLI **and its auth** — mount your credentials in read-only, e.g. for Claude Code:

```yaml
# sketch — a compose `runner` profile could wire this up
services:
  runner:
    profiles: ["runner"]
    build: ./examples/runner/go        # or node / python
    environment:
      OUTBOX_WEBHOOK_SECRET: your-shared-secret
      OUTBOX_MCP_URL: http://outbox-md:8181/mcp   # same compose network
    volumes:
      - ~/.claude:/root/.claude:ro      # mount the CLI's auth into the container
    # the CLI must also be installed in the image
```

Running the runner **on the host** (where your CLI is already installed and logged in) is the simplest path; containerize once you want it hands-off.

---

## The HMAC + event contract

The runner mirrors the server's delivery contract (see the repo [`README.md` §4](../../README.md#4-event-delivery--webhooks--live-updates)).

- **Method/path:** `POST /` (health probe at `GET /healthz`).
- **Headers:**
  - `Content-Type: application/json`
  - `X-Outbox-Event: <event>` — the event name.
  - `X-Outbox-Signature: sha256=<hex>` — HMAC-SHA256 of the **raw body**, present **only when a secret is set**.
- **HMAC verification** (all three runners):
  - Read the **raw body bytes first**, compute `hex(hmac_sha256(secret, body))`, strip the `sha256=` prefix from the header, and **constant-time** compare.
  - Secret set + signature missing/wrong → **`401`**.
  - **No** secret configured → accept (mirrors the server's optional signing).
- **Event filter:** only events in `RUNNER_EVENTS` trigger a run; anything else → **`200`** ignore.
- **Debounce + single-flight:** a burst of events coalesces into **one** agent run (`RUNNER_DEBOUNCE_MS`); if a run is already in flight, exactly one rerun is queued for after it finishes — never two agent invocations at once.

### Configuration (identical across all three runners)

| Env var | Default | Meaning |
|---|---|---|
| `RUNNER_ADDR` | `:8787` | Listen address. |
| `OUTBOX_WEBHOOK_SECRET` | _(unset)_ | Shared HMAC secret. Unset ⇒ signatures not enforced. |
| `RUNNER_EVENTS` | `comment.created,comment.replied` | Comma-separated events to act on. |
| `RUNNER_DEBOUNCE_MS` | `1500` | Debounce window (ms). |
| `RUNNER_AGENT_MODE` | `cli` | `cli` or `api`. |
| `RUNNER_AGENT_CMD` | `claude -p {prompt} --allowedTools mcp__outbox-md__*` | cli-mode command template; `{prompt}` is one argv element. |
| `RUNNER_PROMPT` | _(built-in)_ | Instruction handed to the agent. |
| `RUNNER_AGENT_ID` | `outbox-runner` | Identity recorded on claims/suggestions/replies. |
| `OUTBOX_MCP_URL` | `http://localhost:8181/mcp` | MCP endpoint (api mode). |
| `ANTHROPIC_API_KEY` | _(unset)_ | LLM key (api mode). |
| `ANTHROPIC_MODEL` | `claude-sonnet-4-5` | Model id (api mode), overridable. |

---

## Quick start

> First: server running (`docker compose up`), and for cli mode `claude mcp add ... outbox-md ...`.

### Go

```bash
cd examples/runner/go
go build -o outbox-runner .
OUTBOX_WEBHOOK_SECRET=your-shared-secret ./outbox-runner
# build + test:
go test ./...
```

### Node (stdlib-only)

```bash
cd examples/runner/node
OUTBOX_WEBHOOK_SECRET=your-shared-secret node index.js
# test:
npm test           # = node --test
```

### Python (stdlib-only)

```bash
cd examples/runner/python
OUTBOX_WEBHOOK_SECRET=your-shared-secret python main.py
# test:
python -m unittest
```

Then comment on a doc in the browser — the runner logs `invoking agent` and your agent processes the outbox. To try `api` mode (Go): `RUNNER_AGENT_MODE=api ANTHROPIC_API_KEY=sk-... ./outbox-runner`.

---

## Isolation

Each language lives in its own project so the main outbox-md module stays clean:
`go/` has its **own `go.mod`** (module `outbox-runner`) — the repo's `go build ./...` does not pull it in; `node/` has `package.json`; `python/` has `requirements.txt`. The Node and Python runners are **stdlib-only**, so there is nothing to install to run or test them.
