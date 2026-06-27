# outbox-md — Design

**Date:** 2026-06-27
**Status:** Draft for review
**Type:** Greenfield, open-source

## One-line

A local-first, Dockerized web app for **reading and inline-annotating AI-generated Markdown specs**, where your comments are processed **asynchronously and in order** by any connected AI agent (via MCP) — so feedback improves the doc without ever corrupting it.

## The problem

Agents (superpowers, Claude, GPT, etc.) generate large Markdown specs. Today you read them in a chat window or a mediocre editor, and the only way to give feedback is to type disconnected messages ("change section 3"). The feedback and the artifact live apart. There is no good surface to **annotate the actual text** and iterate a draft into a finished spec.

## North star

**Safe async iteration.** When choices conflict, the winning option is the one that (a) never corrupts the doc and (b) keeps every change ordered and auditable. Speed, cleverness, and feature richness lose to this.

## Core model

1. **The doc is read beautifully.** Rendered Markdown with real typography, navigable, pleasant — the thing missing today. Plus a raw/preview split for editing.

2. **Feedback is inline annotation.** Highlight any text → attach a comment anchored to that range. Comments are the unit of feedback.

3. **Comments are a multi-author annotation layer.** A comment is anchored to a text range and lives in a **thread**. Authors can be **humans, the working agent, or other AIs (council)** — one primitive for all three. Threads support N participants discussing.

4. **An agent processes comments asynchronously, in order.** Nothing the human writes edits the doc directly. A connected agent drains the queue (the "outbox") and, per comment, either:
   - **proposes a suggestion** — a tracked edit (insert/delete) the owner accepts or rejects, or
   - **replies in the thread** — counters bad feedback, asks for clarification, or discusses.

5. **Edit classes.**
   - **Substantive** edits always arrive as **suggestions** (accept/reject).
   - **Mechanical** edits (spelling, grammar, formatting) may **auto-apply** — but only when the user has enabled that class. **Default: off.** The agent self-classifies each edit `mechanical | substantive`.

6. **Resolution authority is a hard rule.** Only the **original comment owner** can resolve their comment. The agent may mark a comment *addressed* and attach a suggestion, but it cannot close the loop. (Generalizes: if an AI council member owns a comment, that member resolves it.)

7. **Council = participants, not a subsystem.** Other AIs (not the working agent) can post comments and discuss/vote **in the same threads**. The human reads that discussion for context before accepting a suggestion or resolving. "Voting" is just a visible multi-party thread surfaced for human judgment.

## Architecture: who runs the AI?

**The app is a substrate, not an AI client.** It owns the docs, comments, threads, suggestions, queue, and version history — and exposes all of it over **MCP**. Any external agent (Claude, GPT, Cursor, …) connects and does the actual thinking. **The app ships zero LLM keys in v1.**

- This is maximally platform-agnostic and cuts enormous scope (no provider adapters, no key management, no LLM orchestration).
- **Trade-off (accepted):** the tool does nothing until an agent is connected — bring-your-own-agent, not a turnkey demo.
- **Fast-follow (not v1):** an optional built-in processor that calls an LLM directly, for turnkey first-run.

## Versioning & merge: internal store, not git

The outbox **serializes** processing — suggestions are reviewed one at a time against current state — so there is **no concurrent branching and no real merge conflict**. We therefore do **not** use git as the engine, and we **never touch the host project's git**.

- The on-disk `.md` file is the **current projection** (current state only).
- A linear **version history** is owned by us: each accepted suggestion / auto-applied edit creates a new version. MD files are tiny, so we snapshot full text per version.
- **Diffs** are computed by us (standard text-diff lib) for display.
- **Accept a suggestion** = write new content to the `.md` file + record a version. No git, no branches.
- **Staleness, not conflicts:** if a suggestion was proposed against version N but N+1 already landed, flag it `stale` and ask the agent to re-propose against current. One integer comparison.
- Works even when the folder is **not** a git repo. Point it at any folder.
- Our metadata lives under a sidecar `.outbox/` dir, auto-added to `.gitignore` so it never pollutes the user's commits. (Alternative: external app-data store keyed by folder path. Default: `.outbox/` + gitignore.)

## Components (the whole v1)

1. **Web editor/reader** — rendered Markdown + raw/preview split; Google-Docs-style margin comments anchored to line/character ranges; suggestion (tracked-change) rendering.
2. **Outbox queue** — visible panel: every comment with status (`open → claimed → addressed/replied → resolved/closed`) and suggestion state (`proposed → accepted/rejected/stale`).
3. **Suggestion/diff view** — review a proposed edit as a tracked change, accept or reject.
4. **MCP server** — the agent seam. Operations: `read_doc`, `list_open_comments`, `post_comment` (anchored), `reply_in_thread`, `propose_suggestion` (classified `mechanical|substantive`), `apply_mechanical_edit` (gated by user toggle), `claim_comment`. **No `resolve` for agents.**
5. **Store** — `.md` files = current state; versions, snapshots, comments, threads, suggestions, and queue state in **SQLite** under `.outbox/`. Zero interaction with host git.

## Data model (multi-author from day 1)

- **Doc** — path, current version pointer.
- **Version** — doc_id, ordinal (linear), full content snapshot, created_by, created_at.
- **Comment** — doc_id, anchor (range against a specific version), author identity (`human | agent | council:<id>`), status, owner (= author; only owner resolves), created_at.
- **ThreadMessage** — comment_id, author identity, body, created_at (replies, discussion, votes).
- **Suggestion** — comment_id, against_version, classification (`mechanical | substantive`), proposed_diff/content, state (`proposed | accepted | rejected | stale`), created_by.

Author identity is a first-class field everywhere a human/agent/council member can act, so council (v1.5) needs no schema change.

## The loop

```
You (web)  ──post anchored comment──▶  Outbox (SQLite)  ◀──claim/list──  Agent (MCP)
                                                                            │
You (web)  ◀── accept/reject suggestion ── Suggestion ◀── propose_suggestion┘
You (web)  ◀────────── thread reply / discussion ──────────────────────────┘
You (web, owner only) ── resolve comment ──▶ closes the loop
```

## Extensibility seams (build now, fill later)

- **Author identity is polymorphic** → council members, multiple agents, and bots all slot in with no schema change.
- **MCP is the only agent interface** → swapping/adding agents = connect another MCP client. Council orchestration and multi-model voting are *more participants*, not new architecture.
- **Edit classification (`mechanical|substantive`) + per-class apply policy** → richer auto-apply policies later (e.g., per-author trust) extend the same gate.
- **Processor is external** → a built-in LLM processor (turnkey mode) is a fast-follow that implements the same MCP operations internally; nothing else changes.
- **Versioning is internal & linear** → an optional git-backed mode (for standalone docs repos) can later be a pluggable store behind the same Version interface.

## Non-goals / deferred

- AI **council orchestration + multi-model voting** → v1.5 (data model already supports it).
- **Built-in LLM processor / turnkey mode** → fast-follow.
- **Real-time multi-user collab / CRDT** → the queue is the concurrency model; not needed.
- **Multi-tenancy / hosted SaaS.**
- **Git as the versioning engine.**
- **Threaded back-and-forth ceremony** beyond what the comment/thread model already gives.

## Open questions (for the plan stage)

- Web stack choice (single container; backend + SPA or server-rendered).
- Exact anchor representation that survives edits (character offsets vs. fuzzy text anchors) — needs care because the doc mutates under comments.
- MCP transport for "any agent" (stdio vs. HTTP/SSE) and how a remote agent reaches a locally-running container.
- Comment-against-version vs. comment-re-anchoring when the doc changes underneath an open comment.
