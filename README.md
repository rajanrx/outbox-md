# outbox-md

> Local-first, agent-agnostic review for AI-generated Markdown specs.

<div align="center">

<a href="https://www.youtube.com/watch?v=CNT49m0xBOY">
  <img src="docs/media/explainer-thumb.png" alt="Watch: What is outbox-md?" width="100%">
</a>

<em>▶ <b><a href="https://www.youtube.com/watch?v=CNT49m0xBOY">What is outbox-md?</a></b> — the 2-minute intro</em>

</div>

Read and inline-annotate AI-generated Markdown in your browser. Your comments **never edit the document directly** — they enter an ordered **outbox** and are processed asynchronously by *any* AI agent connected over **MCP**. The agent proposes a tracked change or replies in a thread; you accept; the file is rewritten and versioned. **The document is never corrupted.**

- **Local-first** — one container pointed at a folder of `.md` files. Nothing leaves your machine.
- **Bring-your-own-agent** — ships **no LLM credentials** and embeds no model. Connect Claude, GPT, or anything that speaks MCP.
- **Safe by construction** — feedback is ordered, edits are tracked changes you approve, and the on-disk file is never silently changed.

---

## How it works

```
        you (browser)                                      your AI agent (runner)
   ┌───────────────────────────┐                    ┌───────────────────────────────┐
   │ select text → comment ────┼───┐                │ claim → propose tracked change │
   │ accept / reply / resolve  │   │                │            or reply            │
   └───────────────────────────┘   │                └───────────────────────────────┘
            ▲                       ▼                         ▲            │
            │              ordered outbox (the queue)         │            │ MCP tools
            │                       │                         │            ▼
            │                  fan events out                 │   (writes the suggestion back)
            │                ┌──────┴───────┐                 │
            └──── SSE ───────┘              └──── webhook ─────┘
              (live; browser                (event-driven; runner
               updates, no refresh)          replaces polling)

                     accept → file rewritten + versioned
```

You comment. Comments enter the ordered outbox. The service **fans each event out** to two sinks: a **webhook** that pushes your AI-agent runner (event-driven — no polling), and **SSE** that updates the browser live (no refresh). The agent claims a comment and proposes a tracked change or replies; you accept, and the `.md` is rewritten and versioned — never edited out from under you.

---

## 1. Run it

**Quickest — run the published image.** No clone, no build; just point the volume at your folder of `.md` specs:

```bash
docker run --rm -p 8181:8181 -v "$PWD/specs:/data" rajanrauniyar/outbox-md
```

Open **http://localhost:8181** — read your specs and start commenting.

- The mounted folder (`-v <folder>:/data`) must be a **folder** of `.md` files, not a single file.
- Port taken? Map a different host port, e.g. `-p 9090:8181`, and use that port everywhere below.
- Multi-arch image — runs natively on Apple Silicon and on amd64 Linux.

<details><summary>From source instead (clone + <code>docker compose</code>, for development)</summary>

Clone the repo, then point it at your specs (defaults to this repo's `docs/specs`):

```bash
OUTBOX_DIR=path/to/your/specs docker compose up -d --build
```

`docker compose pull` fetches the published image; `--build` rebuilds from source. Or build a plain image yourself:

```bash
docker build -t outbox-md .
docker run --rm -p 8181:8181 -v "$PWD/specs:/data" outbox-md
```
</details>

---

## 2. Connect your AI agent (MCP)

outbox-md exposes a **Streamable-HTTP MCP server** — install it in your AI client the same way you'd add any remote MCP server, with one URL:

```
http://localhost:8181/mcp
```

No API key, no config files to hand-edit secrets into — the server ships zero credentials. It just needs to be **running** (step 1).

**Claude Code**
```bash
claude mcp add --transport http outbox-md http://localhost:8181/mcp
# -s project  → commit to this repo's .mcp.json (shared)   |   -s user → all your projects
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
| `propose_suggestion` | Propose a tracked-change edit (full replacement) |
| `reply_in_thread` | Counter, clarify, or discuss instead of editing |

Resolving comments and approving documents stay **human-only** — agents can't accept their own work.

> **Just want to try the loop without wiring up an agent?** Start with `OUTBOX_DEV=1` and the same flow is drivable over plain HTTP (`/api/dev/claim`, `/api/dev/propose`) for testing.

---

## 3. The loop, end to end

1. **You** open the doc, select a sentence, leave a comment. It joins the outbox (the doc is untouched).
2. **Your agent** calls `list_open_comments`, `claim_comment`, then `propose_suggestion` (a tracked change) or `reply_in_thread`.
3. **You** review the suggestion as an inline diff and **Accept** — the `.md` is rewritten and a new version recorded — or reply to push back.
4. When a spec is ready, **Approve** it to pin a baseline. After that, edits become **tracked amendments** that need re-approval, so an approved doc is never silently changed.
5. **History** shows the full decision log — who commented, proposed, edited, and approved, and why.

**Approval gate.** *Approve* and *Re-approve* are blocked until **every comment is resolved**. The server enforces this — an approve with open comments returns **HTTP 409** with a JSON body (`{"error": "cannot approve: N unresolved comment(s) — resolve all comments first"}`). The UI mirrors the rule: the Approve / Re-approve button is **disabled** while comments are unresolved (with a "resolve all N comment(s) first" tooltip), and clicking it opens a **confirmation modal** before the baseline is pinned.

---

## 4. Event delivery — webhooks & live updates

Every governance event fans out to **two sinks**: a **webhook** to your AI-agent runner (machine), and an **SSE** stream to the browser (the UI). Same events, two consumers — the runner acts on them, the UI re-renders from them. Webhooks are covered first; SSE (always on, zero config) is in **[Live updates (SSE)](#live-updates-sse)** below.

### Webhooks (replace polling)

Instead of polling `list_open_comments`, point outbox at an HTTP **runner** and it will **push** a notification the moment something needs work. Webhooks are optional and off by default.

**Enable it** — set a URL (and optionally a signing secret) via env or `outbox.yaml`:

```bash
OUTBOX_WEBHOOK_URL=https://your-runner.example/hook \
OUTBOX_WEBHOOK_SECRET=your-shared-secret \
docker compose up -d --build
```

```yaml
# outbox.yaml (in your OUTBOX_DIR) — env vars override these
webhook:
  url: https://your-runner.example/hook
  secret: your-shared-secret
  events: [comment.created, comment.replied, comment.resolved, document.approved]  # omit ⇒ all enabled
```

Env (`OUTBOX_WEBHOOK_URL`, `OUTBOX_WEBHOOK_SECRET`) wins over the file. An empty `url` disables webhooks entirely; an empty/omitted `events` list means **all events are enabled**.

**Events** (one POST per event):

| Event | Fires when |
|---|---|
| `comment.created` | a human posts a new comment |
| `comment.replied` | a human replies again on a comment (also re-opens it for the agent) |
| `comment.resolved` | a human resolves a comment |
| `document.approved` | a document is approved **or** re-approved |

**The POST contract** — `Content-Type: application/json`, plus:

- `X-Outbox-Event: <event>` — the event name (also in the body).
- `X-Outbox-Signature: sha256=<hex>` — present **only when a secret is set**. It is the HMAC-SHA256 of the **raw request body** keyed by the secret. Verify by recomputing `hex(hmac_sha256(secret, body))` over the bytes you received and comparing against the header value (constant-time).

Example body:

```json
{
  "event": "comment.created",
  "docId": "0f9c…",
  "docPath": "specs/auth.md",
  "commentId": "7ab2…",
  "anchor": { "start": 120, "end": 156 },
  "excerpt": "the exact anchored text the comment refers to",
  "thread": [{ "id": "…", "commentId": "7ab2…", "authorIdentity": "human", "body": "please clarify X" }],
  "ts": "2026-06-30T12:00:00Z"
}
```

`commentId`, `anchor`, `excerpt`, and `thread` are omitted for `document.approved` (it carries only `docId`/`docPath`/`ts`).

**Delivery semantics** — fire-and-forget: delivery runs on a background goroutine and **never blocks or fails** the originating action. Each POST uses a **5s** client timeout and retries up to **2 times** (after ~200ms then ~800ms) on a transport error or non-2xx response; a final failure is logged to stderr. Treat events as **at-least-once** and best-effort, not guaranteed — the UI/MCP state remains the source of truth.

**Build a webhook-driven runner.** The webhook replaces the poll loop with an event loop:

> `comment.created` / `comment.replied` arrives → runner calls **`list_open_comments`** → **`claim_comment`** → **`propose_suggestion`** (a tracked-change edit) or **`reply_in_thread`**.

That's the same MCP toolset from step 2 — just triggered by a push instead of a timer. **Bring your own credentials:** the outbox server ships **zero** LLM keys; your runner holds the model key and does the reasoning, then writes back through MCP. The signature header lets the runner trust that a request really came from your outbox instance.

> **Don't want to build one?** A reference **autonomous runner** ships in [`examples/runner/`](examples/runner/README.md) — Go, Node, and Python, each verifying the signature and driving the claim → propose/reply loop for you. Where the **interactive MCP** above is human-driven (you ask an agent to process the outbox in a chat session), the runner is webhook-driven and hands-off; its default CLI mode reuses your existing coding-agent subscription, so there's **no per-token API cost**.

### Live updates (SSE)

The browser is the **second sink** for the same events. The UI subscribes — automatically, no setup — to a **Server-Sent Events** stream and re-renders the instant something changes, so a comment, reply, resolution, or approval shows up **without a manual refresh**.

```
GET /api/events     →     Content-Type: text/event-stream
```

The stream emits one frame per governance event — the same four as the webhook (`comment.created`, `comment.replied`, `comment.resolved`, `document.approved`) — as `event: <name>` with a `data:` JSON body (the same payload shape as the webhook). `: connected` and `: ping` comment lines (a ~25s heartbeat) keep the connection alive and are ignored by the client. The UI opens this on load and refreshes the affected document on each event; if the stream drops, the browser reconnects automatically and a slow background poll (~15s) covers any gap. No credentials, no config — it's on whenever the server is running.

Together the two sinks are one **event-delivery** story: **webhook = your machine/runner**, **SSE = your browser**. The same event drives the agent loop and the live UI.

---

## What's inside

- **Reader** — rendered Markdown (GFM, syntax highlighting, Mermaid) with select-to-comment.
- **Comments & threads** — anchored to the exact text; discuss, counter, resolve.
- **Suggestions** — agent edits shown as inline diffs you accept or reject.
- **Governance** — `draft → approved → amending → approved`: approve to pin a baseline; post-approval edits accumulate as amendments until you re-approve.
- **Decision log** — a per-document **History** timeline of every comment, proposal, edit, and approval.
- **Event delivery** — every governance event fans out to a **webhook** (your AI-agent runner) and an **SSE** stream (the browser updates live, no refresh).

---

## Status & limitations

Past walking-skeleton: the review loop, governance, and audit log all work and are covered by tests. Honest caveats before you rely on it:

- **Local-first & unauthenticated** — designed for a single user on `localhost`. **Don't expose the port to a network** without putting auth in front of it (see [`SECURITY.md`](SECURITY.md)).
- **Supervise long agent runs** — if an agent claims comments and crashes, those claims aren't auto-recovered yet (no reaper). Fine while you're watching; not yet fire-and-forget.
- **Agents respond, they don't initiate** — an agent acts on comments *you* raise; it can't open new ones (AI-council is on the roadmap).

---

## Watch & learn

<div align="center">
<table>
<tr>
<td width="50%" valign="top">
  <a href="https://www.youtube.com/watch?v=4VH7NT095ms"><img src="docs/media/tutorial-thumb.png" alt="Tutorial" width="100%"></a>
  <p align="center"><b>▶ <a href="https://www.youtube.com/watch?v=4VH7NT095ms">Using outbox-md</a></b><br/>Run it → comment → connect an agent → accept</p>
</td>
<td width="50%" valign="top">
  <a href="https://www.youtube.com/watch?v=VmuwLniMU9M"><img src="docs/media/deepdive-thumb.png" alt="Deep dive" width="100%"></a>
  <p align="center"><b>▶ <a href="https://www.youtube.com/watch?v=VmuwLniMU9M">Architecture &amp; Vision</a></b><br/>The hard parts and where it's headed — for builders</p>
</td>
</tr>
</table>
</div>

## Design

- Core design: [`docs/specs/2026-06-27-outbox-md-design.md`](docs/specs/2026-06-27-outbox-md-design.md)
- Governance seam: [`docs/specs/2026-06-28-governance-seam-design.md`](docs/specs/2026-06-28-governance-seam-design.md)
- Decision log: [`docs/specs/2026-06-30-decision-log-design.md`](docs/specs/2026-06-30-decision-log-design.md)

## License

MIT — see [LICENSE](LICENSE).
