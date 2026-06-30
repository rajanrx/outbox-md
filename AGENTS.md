# Working the outbox (for AI agents)

> If you are an AI agent connected to outbox-md over MCP, this file is your operating manual. A human has written **comments** on a Markdown spec; your job is to process them — propose tracked-change edits or reply in the thread — without ever editing the file directly. The human accepts your work; only then is the `.md` rewritten and versioned.

The same workflow is shipped **with the server** as an MCP prompt named `process_outbox` — in Claude Code it appears as `/mcp__outbox-md__process_outbox`. Pull it any time to reload these steps.

---

## The loop

```
list_open_comments  →  read the excerpt + thread (the feedback)
        │
        ▼
claim_comment  →  get a claim token
        │
        ▼
propose_suggestion (a tracked-change edit)   OR   reply_in_thread (counter / clarify / discuss)
        │
        ▼
the human accepts → the .md is rewritten and a new version is recorded
```

Work the queue **in order**, and never claim more comments at once than the configured batch size (default **5**; an over-claim is rejected with a clear error telling you the cap).

> **Polling vs. push.** This manual is written for a polling agent (re-run `list_open_comments`). The server can also **push** a webhook the instant a comment is created/replied/resolved (or a doc is approved), so a runner can trigger this exact loop event-driven instead of on a timer — the recommended setup for a long-running agent. See **[Event delivery — webhooks & live updates](README.md#4-event-delivery--webhooks--live-updates)** in the README (the browser UI gets the same events live over SSE). Note: a fresh human reply re-opens a comment you already handled, so it reappears in `list_open_comments` for another pass.

---

## Step 1 — read the feedback

Call `list_open_comments`. Each entry carries everything you need to understand the request — both **where** the human is pointing and **what** they wrote:

```jsonc
{
  "id": "4897e6e8…",
  "docId": "d8b2503a…",
  "docPath": "docs/specs/2026-06-27-booking.md", // which file
  "againstVersionId": "a3416b09…",
  "anchor": { "start": 431, "end": 457 },        // rune offsets into that version
  "excerpt": "A confirmed multi-provider",        // the EXACT text they flagged
  "status": "open",
  "owner": "human",
  "authorIdentity": "human",
  "postApproval": false,
  "thread": [                                      // the discussion — the human's note is here
    { "authorIdentity": "human", "body": "This is overstated — AssignBooking already swaps a provider." }
  ]
}
```

The human's actual feedback lives in **`thread`** (the first message is what they typed when they raised the comment); **`excerpt`** is the document text they anchored it to. Read both before acting — together they tell you what to change and why. Use `read_doc` when you need the full surrounding document for context.

## Step 2 — claim it

Call `claim_comment` with the comment id. You get back a **claim token**. Pass that token on every subsequent action for this comment. Claim only what you can work now, within the batch-size cap.

## Step 3 — respond

Pick one:

- **`propose_suggestion`** — when the fix is an edit. Provide the **full replacement document content** (not a patch); outbox-md renders it to the human as a tracked-change diff. Keep the change minimal and faithful to the feedback.
- **`reply_in_thread`** — when you need to counter, ask a clarifying question, or discuss instead of editing. Your reply appears under the human's comment.

**Council mode** (driven by the webhook runner): instead of the two above, a council member submits a single lensed review with **`submit_review`** (a `lens`, a `verdict` of `edit` / `reply` / `reject_comment`, a `rationale`, and full replacement `content` iff the verdict is `edit`). Each member's review becomes one **candidate** among N; the human picks. `submit_review` never resolves or writes — it only records a candidate.

Either way, include the claim token and your agent identity.

---

## How to reply well — stay unbiased

You have a strong built-in pull toward agreement. **Resist it.** An agent that rubber-stamps every comment with *"you're absolutely right, here's the change"* quietly corrupts the spec. The human raised a point to be **evaluated**, not obeyed.

- **A comment is not an order.** The right response may be an edit, a clarifying question, **or a reasoned disagreement** (`reply_in_thread`). Don't default to proposing a change.
- **No sycophancy.** Engage on technical merit. If the human's premise is wrong, say so — with evidence from the document — instead of agreeing to be agreeable.
- **Verify before you propose.** Check the claim against the actual content (`excerpt`, and `read_doc` when you need more). Never invent facts to justify an edit.
- **Minimal, faithful edits.** Preserve the author's voice and scope. Change only what the feedback requires; don't rewrite around it.
- **Surface uncertainty.** If you're unsure what's meant, ask — don't guess a large rewrite.
- **Judge on merit, not on who said it.** Anchor in the document's own content and the project's constraints. This guards against *both* caving to the commenter and reflexively defending the existing text.

When you disagree, disagree usefully: name the specific technical reason, point to the relevant text, and offer the alternative. The human still decides — your job is an honest, grounded opinion, not a yes.

---

## What you must NOT do

These are **human-only** — there are no MCP tools for them, by design, so an agent cannot accept its own work:

- **Resolve** a comment
- **Accept / reject** a suggestion
- **Pick** a council candidate
- **Approve / re-approve** a document

You propose and discuss; the human decides. After a document is **approved**, your suggestions become tracked **amendments** that need the human's re-approval — an approved spec is never silently changed.

---

## Tool reference

| Tool | What you do | Human-only? |
|---|---|---|
| `read_doc` | Read a document's content + lifecycle status | — |
| `list_open_comments` | See the ordered outbox **with each comment's excerpt + thread (feedback)** | — |
| `claim_comment` | Claim comment(s) → receive a claim token | — |
| `propose_suggestion` | Propose a tracked-change edit (full replacement) | — |
| `reply_in_thread` | Counter, clarify, or discuss | — |
| `submit_review` | Council mode: record one lensed review (verdict + rationale, edit content iff `edit`) as a candidate | — |
| Resolve / Accept / Pick / Approve | — | **human (UI only)** |

---

## Quick start in Claude Code

```bash
claude mcp add --transport http outbox-md http://localhost:8181/mcp
# then, in the session:
/mcp__outbox-md__process_outbox      # loads this workflow
```

See [`README.md`](README.md) for installing the MCP in other clients.
