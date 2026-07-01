# outbox-md — Setup & Usage Guide

Everything beyond the [README](README.md) quickstart: installing, connecting agents, reviewing multiple projects, scoping folders, staying up to date, and running the hands-off automation.

- [Install](#install)
- [`outbox` commands & flags](#outbox-commands--flags)
- [Connect your agent (MCP)](#connect-your-agent-mcp)
- [The review loop](#the-review-loop)
- [Multiple projects](#multiple-projects)
- [Serving part of a repo (`sources`)](#serving-part-of-a-repo-sources)
- [Staying up to date](#staying-up-to-date)
- [Automation: webhooks & a hands-off runner](#automation-webhooks--a-hands-off-runner)

---

## Install

**Binary (recommended — macOS + Linux, amd64 + arm64):**

```bash
curl -fsSL https://raw.githubusercontent.com/rajanrx/outbox-md/main/install.sh | sh
```

The installer downloads a prebuilt binary from the latest [GitHub Release](https://github.com/rajanrx/outbox-md/releases), verifies its `checksums.txt`, and installs to `/usr/local/bin` (or `~/.local/bin`). Prefer to read it first? `curl -fsSL …/install.sh | less`.

**Docker (no install):**

```bash
docker run --rm -p 8181:8181 -v "$PWD/specs:/data" rajanrauniyar/outbox-md
```

The mounted path must be a **folder** of `.md` files, not a single file. Port taken? Map another host port (`-p 9090:8181`). The image is multi-arch (Apple Silicon + amd64 Linux).

<details>
<summary>From source (<code>docker compose</code>) or building your own image</summary>

```bash
# clone, then point at your specs (defaults to this repo's docs/specs)
OUTBOX_DIR=path/to/your/specs docker compose up -d --build

# or build a plain image
docker build -t outbox-md .
docker run --rm -p 8181:8181 -v "$PWD/specs:/data" outbox-md
```

`docker compose pull` fetches the published image instead of building.
</details>

---

## `outbox` commands & flags

| Command | What it does |
|---|---|
| `outbox up` | Serve the review UI + MCP, then open the browser (the everyday command). |
| `outbox serve` | Same, without opening a browser (also the default with no arguments — this is what the Docker image runs). |
| `outbox init` | Scaffold `outbox.yaml` and register the MCP endpoint with the Claude CLI in the current folder. |
| `outbox upgrade` | Update to the latest release (self-update). |
| `outbox version` | Print the CLI version. |
| `outbox help` | Show usage. |

`serve` and `up` take:

- `-dir` — folder of `.md` files to serve (default `.`; the Docker image sets `/data`).
- `-addr` — listen address (default `:8181`).

Precedence is **flag > `OUTBOX_DIR` / `OUTBOX_ADDR` env > default**.

---

## Connect your agent (MCP)

outbox-md exposes a **Streamable-HTTP MCP server** at `http://localhost:8181/mcp`. No API key, no secrets to hand-edit — it just needs to be **running**. `outbox init` registers it with the Claude CLI automatically; to do it by hand or use another client:

**Claude Code**
```bash
claude mcp add --transport http outbox-md http://localhost:8181/mcp
# -s project → commit to this repo's .mcp.json (shared)   |   -s user → all your projects
claude mcp list   # should show: outbox-md ✓ connected
```

**Claude Desktop / Cursor / Windsurf** (and other config-file clients) — add to the `mcpServers` block:
```json
{
  "mcpServers": {
    "outbox-md": { "url": "http://localhost:8181/mcp" }
  }
}
```

<details><summary>Stdio-only client? Bridge it with <code>mcp-remote</code></summary>

```json
{
  "mcpServers": {
    "outbox-md": { "command": "npx", "args": ["-y", "mcp-remote", "http://localhost:8181/mcp"] }
  }
}
```
</details>

Once connected, your agent gets five tools:

| Tool | What the agent does |
|---|---|
| `read_doc` | Read a document's content + lifecycle status |
| `list_open_comments` | See the ordered outbox of feedback awaiting work |
| `claim_comment` | Claim comment(s) to work on → gets a token |
| `propose_suggestion` | Propose a tracked-change edit |
| `reply_in_thread` | Counter, clarify, or discuss instead of editing |

Resolving comments and approving documents stay **human-only** — agents can't accept their own work.

> **Just want to try the loop without wiring up an agent?** Start with `OUTBOX_DEV=1` and drive the same flow over plain HTTP (`/api/dev/claim`, `/api/dev/propose`) for testing.

---

## The review loop

1. **You** open the doc, select a sentence, leave a comment. It joins the outbox — the doc is untouched.
2. **Your agent** calls `list_open_comments` → `claim_comment` → `propose_suggestion` (a tracked change) or `reply_in_thread`.
3. **You** review the suggestion as an inline diff and **Accept** (the `.md` is rewritten and a new version recorded) or reply to push back. Each addressed comment shows a compact diff excerpt with a **See diff** button that opens the full single-file change; the modal's **Folder changes** tab lists every other doc across the project with a pending suggestion, each as its own current-vs-proposed diff — built entirely from outbox-md's own data (no git required). Accept still applies just the one suggestion you opened.
4. **Approve** a finished spec to pin a baseline. After that, edits become **tracked amendments** that need re-approval, so an approved doc is never silently changed.
5. **History** shows the full decision log — who commented, proposed, edited, and approved, and why.

**Approval gate.** *Approve* and *Re-approve* are blocked until **every comment is resolved**. The server enforces this — an approve with open comments returns **HTTP 409** (`{"error": "cannot approve: N unresolved comment(s) …"}`). The UI mirrors it: the button is disabled with a tooltip until you're clear, and clicking it opens a confirmation modal before the baseline is pinned.

---

## Multiple projects

Each running `outbox` serves **one folder on one port** (`:8181` by default). Two ways to review more than one project today:

**A. One folder per project, on separate ports** — simplest for a couple of projects:

```bash
cd ~/project-a && outbox up                 # → http://localhost:8181
cd ~/project-b && outbox up --addr :8182    # → http://localhost:8182
```

Each is its own server, browser tab, and MCP registration.

**B. One server over a parent folder** — best when your projects live together. Serve the parent and list each project's docs in a single `outbox.yaml`:

```yaml
# ~/reviews/outbox.yaml
sources:
  - project-a/docs
  - project-b/specs
```

```bash
cd ~/reviews && outbox up    # one UI, one MCP, both projects visible
```

> **Note:** `sources` paths must live **under** the served folder — the whitelist deliberately can't reach outside it. So option B needs your projects inside (or symlinked into) that one parent folder.
>
> A first-class **project switcher** — register projects anywhere on disk and switch in the UI — is on the roadmap.

---

## Serving part of a repo (`sources`)

By default outbox-md ingests **every** `.md` file under the served folder. To serve only part of a larger repo, list a whitelist of folders and/or globs (relative to the folder) in `outbox.yaml`:

```yaml
sources:
  - docs/specs        # a folder → walked recursively
  - rfcs              # another folder
  - drafts/*.md       # a glob → matched files only (non-recursive)
```

Omit `sources` (or leave it empty) to serve everything. Entries that escape the folder are rejected. `OUTBOX_SOURCES` (comma-separated) overrides the file, e.g. `OUTBOX_SOURCES=docs/specs,rfcs`.

`sources` is enforced **when serving**, not just on import: narrowing it hides out-of-whitelist docs from the UI, the HTTP API, and MCP agents (`list_open_comments`/`read_doc`) alike — without deleting their comments or history. Widen it again and they reappear.

---

## Staying up to date

- **Binary (curl/direct install):** `outbox up` **auto-updates by default** — it checks for a newer release (at most once a day) and, if found, self-updates and restarts. Turn it off with `auto_update: false` in `outbox.yaml` (or `OUTBOX_AUTO_UPDATE=false`); you can still update on demand with **`outbox upgrade`**.
- **Homebrew:** `brew upgrade outbox-md` (a brew-managed binary won't self-update — it points you here).
- **Docker:** the container binary can't self-update — pull a new image (`docker compose pull && docker compose up -d`), or enable the commented **Watchtower** service in `docker-compose.yml`. Pin the image to a major tag (`:0`) so it applies `0.x` minors/patches but not a breaking major.

---

## Automation: webhooks & a hands-off runner

The interactive MCP loop is human-driven (you ask an agent to process the outbox in a chat session). For a **hands-off** loop, outbox can **push** a webhook the moment a comment needs work, and a **runner** drives claim → propose/reply automatically.

A reference **autonomous runner** ships in [`examples/runner/`](examples/runner/README.md) — Go, Node, and Python, each verifying the signature and driving the loop for you. Its default CLI mode reuses your existing coding-agent subscription, so there's **no per-token API cost**. A turnkey ops layer wraps the image + runner:

```bash
make up        # start the server (docker compose) → http://localhost:8181
make runner    # start a webhook runner detached → runner.log  (RUNNER_LANG=python|go|node)
make status    # server containers + whether the runner port is listening
make logs      # tail the runner
```

Run `make` on its own for the full menu. To keep the runner alive across reboots (**launchd** on macOS, **systemd --user** on Linux) and for the server → runner webhook wiring, see [`deploy/README.md`](deploy/README.md).

<details>
<summary><b>Webhook contract</b> — enable, events, signature, payload, delivery</summary>

Enable by setting a URL (+ optional signing secret) via env or `outbox.yaml` (env wins; an empty URL disables webhooks):

```yaml
# outbox.yaml
webhook:
  url: https://your-runner.example/hook
  secret: your-shared-secret
  events: [comment.created, comment.replied, comment.resolved, document.approved]  # omit ⇒ all enabled
```

**Events** (one `POST` per event):

| Event | Fires when |
|---|---|
| `comment.created` | a human posts a new comment |
| `comment.replied` | a human replies again on a comment (re-opens it for the agent) |
| `comment.resolved` | a human resolves a comment |
| `document.approved` | a document is approved or re-approved |

**The POST contract** — `Content-Type: application/json`, plus:

- `X-Outbox-Event: <event>` — the event name (also in the body).
- `X-Outbox-Signature: sha256=<hex>` — present **only when a secret is set**; the HMAC-SHA256 of the **raw request body** keyed by the secret. Verify by recomputing `hex(hmac_sha256(secret, body))` and comparing constant-time.

```json
{
  "event": "comment.created",
  "docId": "0f9c…", "docPath": "specs/auth.md",
  "commentId": "7ab2…", "anchor": { "start": 120, "end": 156 },
  "excerpt": "the exact anchored text the comment refers to",
  "thread": [{ "authorIdentity": "human", "body": "please clarify X" }],
  "ts": "2026-06-30T12:00:00Z"
}
```

`commentId`/`anchor`/`excerpt`/`thread` are omitted for `document.approved` (it carries only `docId`/`docPath`/`ts`).

**Delivery** — fire-and-forget on a background goroutine; **never blocks or fails** the originating action. 5s timeout, up to 2 retries (~200ms then ~800ms) on transport error or non-2xx; a final failure is logged. Treat events as **at-least-once** and best-effort — the UI/MCP state is the source of truth. The webhook fires only on **action-needed** events, so the agent's own reply or suggestion never re-triggers a run.
</details>

**Live updates (SSE).** Independently of webhooks, the browser stays live over a **Server-Sent Events** stream (`GET /api/events`) with zero config — comments, replies, resolutions, approvals, *and* the agent's own writes (`comment.updated`, `suggestion.proposed`) all appear without a refresh. On a dropped connection the browser reconnects and a slow background poll (~15s) covers any gap.
