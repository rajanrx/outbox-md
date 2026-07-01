# outbox-md

> Local-first, agent-agnostic review for AI-generated Markdown specs.

<div align="center">

<a href="https://www.youtube.com/watch?v=CNT49m0xBOY">
  <img src="docs/media/explainer-thumb.png" alt="Watch: What is outbox-md?" width="100%">
</a>

<em>▶ <b><a href="https://www.youtube.com/watch?v=CNT49m0xBOY">What is outbox-md?</a></b> — the 2-minute intro</em>

</div>

Read and inline-annotate AI-generated Markdown in your browser. Your comments **never edit the document directly** — they enter an ordered **outbox** and are handled by *any* AI agent you connect over **MCP**. The agent proposes a tracked change or replies; you accept; the file is rewritten and versioned. **The document is never corrupted.**

- **Local-first** — points at a folder of `.md` files on your machine. Nothing leaves it.
- **Bring-your-own-agent** — ships **no LLM credentials**. Connect Claude, GPT, or anything that speaks MCP.
- **Safe by construction** — feedback is ordered, edits are tracked changes you approve, the on-disk file is never silently changed.

---

## Quickstart

**1. Install** (macOS + Linux) — Homebrew or the install script:

```bash
brew install rajanrx/tap/outbox-md
# or, without Homebrew:
curl -fsSL https://raw.githubusercontent.com/rajanrx/outbox-md/main/install.sh | sh
```

**2. Point it at a folder of `.md` specs and start:**

```bash
cd path/to/your/specs
outbox init             # scaffold outbox.yaml + auto-wire the MCP with your installed AI clients
outbox up --auto-reply  # serve the review UI, open it, and auto-reply to your comments
```

> `outbox init` auto-wires Claude Code, Gemini CLI, Cursor, Windsurf, Claude Desktop, and Codex if installed. See [Supported clients](SETUP.md#supported-clients).
>
> **`--auto-reply`** runs a hands-off in-process agent: on each comment you leave, it spawns your Claude CLI (your subscription — no API cost) to reply, reacting only to *your* comments, never its own. Drop the flag for **interactive-only** (a plain `outbox up`, where you drive an agent yourself in a chat). Details: [hands-off auto-reply](SETUP.md#hands-off-auto-reply-in-process-no-runner).

**3. Connect your agent** — if `init` didn't do it automatically, add this MCP endpoint to your AI client (one URL, no API key):

```
http://localhost:8181/mcp
```

**4. Review** — select a sentence, leave a comment. Your agent picks it up, proposes a tracked change, and you **Accept** — the `.md` is rewritten and versioned. That's the loop.

> ### 📖 [Setup & Usage Guide →](SETUP.md)
> Docker · **multiple projects** · other agents (Cursor / Claude Desktop / …) · `sources` scoping · hands-off automation & runners · all commands. Everything beyond the quickstart lives there.

---

## Commands

| Command | What it does |
|---|---|
| `outbox up` | Serve the review UI + MCP, then open it in your browser (the everyday command). |
| `outbox up --auto-reply` | Same, plus a **hands-off** in-process agent that replies to your comments automatically — opt-in, reuses your Claude CLI subscription (no API cost), reacts only to your comments. See [Setup](SETUP.md#hands-off-auto-reply-in-process-no-runner). |
| `outbox serve` | Same, without opening a browser (the default with no arguments; what the Docker image runs). |
| `outbox init` | Scaffold `outbox.yaml` and register the MCP with your installed AI client(s) in this folder. |
| `outbox add [path]` · `remove` · `projects` | Register / unregister / list projects — review several folders from one server. |
| `outbox upgrade` | Update to the latest release (self-update). |
| `outbox version` · `outbox help` | Print the version / usage. |

`serve` and `up` take `-dir` (folder to serve, default `.`), `-addr` (listen address, default `:8181`), and `-auto-reply` (opt-in hands-off agent, default off). Precedence is **flag > `OUTBOX_DIR` / `OUTBOX_ADDR` / `OUTBOX_AUTO_REPLY` env > default**.

Full detail — install options, connecting each client, multiple projects, `sources` scoping, automation — is in the **[Setup & Usage Guide](SETUP.md)**.

---

## How it works

You comment on a doc; the comment enters an **ordered outbox** instead of touching the file. The server notifies your agent (over MCP) and updates your browser live. The agent **claims** a comment and either **proposes a tracked change** or **replies**; you **accept**, and only then is the `.md` rewritten and a new version recorded. Resolving comments and approving docs stay **human-only** — an agent can't accept its own work.

```
   you (browser)                              your AI agent
  ┌──────────────────┐                   ┌──────────────────────┐
  │ comment / accept │──▶ ordered outbox │ claim → propose /    │
  │ reply / resolve  │◀── live (SSE) ────│ reply  (via MCP)     │
  └──────────────────┘                   └──────────────────────┘
                   accept → file rewritten + versioned
```

---

## Status & limitations

The review loop, governance, and audit log all work and are covered by tests. Honest caveats:

- **Local-first & unauthenticated** — built for a single user on `localhost`. **Don't expose the port** without auth in front (see [`SECURITY.md`](SECURITY.md)).
- **Supervise long agent runs** — a crashed agent's claims aren't auto-recovered yet (no reaper).
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

MIT — see [LICENSE](LICENSE). Contributions welcome — see [`CONTRIBUTING.md`](CONTRIBUTING.md).
