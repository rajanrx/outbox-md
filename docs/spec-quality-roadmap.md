# outbox-md — Spec-Quality Platform (Design / Roadmap)

| | |
|---|---|
| **Status** | Draft — pending approval |
| **Cycle** | v2 · pluggable spec-quality platform |
| **Date** | 2026-06-30 |
| **Branch** | `docs/spec-quality-roadmap` |
| **Builds on** | `2026-06-27-outbox-md-design.md`, `2026-06-28-governance-seam-design.md`, `2026-06-30-decision-log-design.md`, and the in-flight webhooks cycle |

## 1. Vision

Today outbox-md reviews **flat Markdown**: a human inline-comments a `.md`, comments queue in an ordered outbox, one MCP agent proposes a tracked change or replies in a thread, the human accepts, the file is rewritten and versioned. The loop is deliberately small and its invariants are load-bearing:

- **Local-first** — one container over a folder of `.md` files.
- **Bring-your-own-agent (BYO)** — the server ships **zero LLM credentials** and embeds no model.
- **Safe-by-construction** — feedback is ordered, edits are tracked changes the human approves, resolve/accept/approve are **human-only**, and the on-disk file is never silently changed.

This document plans the evolution from "a reviewer for flat Markdown" to **a pluggable spec-quality platform**, without touching those invariants. The platform is four layers:

```
┌──────────────────────────────────────────────────────────────────────┐
│ (a) FORMAT ADAPTERS                                                   │
│     flat .md  │  OpenSpec change  │  RFC  │  ADR  │  …                │
│     how a file (or folder) maps onto Document / Comment / Suggestion  │
├──────────────────────────────────────────────────────────────────────┤
│ (b) COMMENT SOURCES (what seeds the outbox)                          │
│     human inline  │  linters / validators  │  AI council             │
│     all land as ordered Comments carrying provenance                 │
├──────────────────────────────────────────────────────────────────────┤
│ (c) AGENT-ORCHESTRATION CONTRACT                                     │
│     single agent (MCP)  OR  council (N agents, webhook runner)       │
│     BYO preserved: credentials + model orchestration live OFF-server │
├──────────────────────────────────────────────────────────────────────┤
│ (d) SAFE-BY-CONSTRUCTION CORE  (unchanged)                          │
│     ordered outbox · tracked changes · human-only accept/approve ·   │
│     versioned, never-silently-corrupted on-disk file                 │
└──────────────────────────────────────────────────────────────────────┘
```

The core (d) is the part we **do not** rewrite. Every feature below is expressed as: a new *adapter*, a new *comment source*, or a new *orchestration shape* feeding the same accept/approve seam. If a feature appears to require the server to call a model or hold an API key, that is the signal it has drifted off the BYO invariant and must move to the webhook runner.

### 1.1 What each layer reuses

| Layer | Reuses today's core |
|---|---|
| (a) adapters | `Document`/`Version`/`Comment`/`Suggestion`; anchor offsets; approve/re-approve seam |
| (b) sources | `Comment.authorIdentity` / `Comment.owner` already carry provenance; ordered outbox |
| (c) orchestration | MCP tools + the webhooks cycle (events → external runner reacts) |
| (d) core | no change |

---

## 2. OpenSpec support (format adapter)

### 2.1 What OpenSpec actually is (confirmed)

OpenSpec ([`Fission-AI/OpenSpec`](https://github.com/Fission-AI/OpenSpec), [openspec.dev](https://openspec.dev/), MIT, `@fission-ai/openspec` on npm) is a **spec-driven-development** framework for AI coding agents — not OpenAPI. `openspec init` scaffolds:

```
openspec/
├── specs/                       # source of truth: how the system behaves now
│   └── <capability>/spec.md     # ## Purpose + ## Requirements
├── changes/                     # proposed modifications, one folder each
│   └── <change-id>/
│       ├── proposal.md          # ## Why · ## What Changes · ## Capabilities · ## Impact
│       ├── tasks.md             # ## N. Group → "- [ ] N.M …" checkboxes
│       ├── design.md            # optional: Context/Goals/Decisions/Risks
│       └── specs/<capability>/spec.md   # DELTA specs (not full specs)
└── config.yaml                  # project configuration
```

> **Precision notes (confirmed against the repo, not the task's hypotheses):**
> - There is **no `project.md`** in current OpenSpec; the project file is `openspec/config.yaml`. (`project.md` was an older layout.)
> - OpenSpec is mid-migration to a new artifact-guided "opsx" workflow (`/opsx:propose` → `/opsx:apply` → `/opsx:archive`), but the on-disk artifacts (proposal/specs/design/tasks + delta specs) are unchanged — that is what outbox-md adapts to.
> Sources: [`schemas/spec-driven/schema.yaml`](https://github.com/Fission-AI/OpenSpec/blob/main/schemas/spec-driven/schema.yaml), [`docs/concepts.md`](https://github.com/Fission-AI/OpenSpec/blob/main/docs/concepts.md), [`docs/cli.md`](https://github.com/Fission-AI/OpenSpec/blob/main/docs/cli.md), [`src/core/validation/validator.ts`](https://github.com/Fission-AI/OpenSpec/blob/main/src/core/validation/validator.ts).

**Spec / delta format** (the bytes outbox-md must understand):

- A **spec** (`openspec/specs/<cap>/spec.md`) has `## Purpose` and `## Requirements`. Each requirement is `### Requirement: <name>` with normative **SHALL/MUST** text, and **every requirement has ≥1** `#### Scenario:` block (WHEN/THEN, GIVEN/AND). **Scenarios MUST use exactly 4 hashtags** — 3 hashtags or a bullet list "fails silently" in OpenSpec's own words.
- A **delta spec** (`changes/<id>/specs/<cap>/spec.md`) is *not* a full spec. It uses operation headers — `## ADDED Requirements`, `## MODIFIED Requirements`, `## REMOVED Requirements`, `## RENAMED Requirements` — each containing requirement blocks. MODIFIED must carry the **entire** updated requirement block; REMOVED is **names only** (the template *asks* for `**Reason**`/`**Migration**`, but the validator enforces names only); RENAMED uses FROM:/TO:.

**`openspec validate` (what it actually checks)** — from `validator.ts` + `constants.ts`:

- Change ERRORs: missing `## Why`/`## What Changes`; **Why < 50 chars**; **≥1 delta** required; ADDED/MODIFIED requirement missing text, missing **SHALL/MUST**, or **0 scenarios**; duplicate requirement names within a section; malformed deltas.
- Spec ERRORs: empty name/purpose; **≥1 requirement**; requirement missing SHALL/MUST or scenarios.
- WARNINGs: purpose < 50 chars; requirement text > 500 chars; **> 10 deltas** per change; delta description too brief.
- **`--strict`** flips the gate: by default a report is valid iff `errors === 0`; under `--strict` it is valid iff `errors === 0 && warnings === 0` — i.e. **`--strict` promotes every warning to blocking**. (`validator.ts:396`.)

**Change lifecycle:** propose (`proposal.md`) → specs/design/tasks → implement (check off `tasks.md`) → **archive**: `openspec archive` validates, **merges the delta specs into `openspec/specs/`**, and moves the change folder to `openspec/changes/archive/YYYY-MM-DD-<id>/`. `--skip-specs` exists for tooling/doc-only changes. This is the crux for the adapter (§2.3).

### 2.2 The impedance: a change is a *folder*, our model is a *file*

outbox-md's model is single-file: a `Comment` carries one `Anchor` (rune offsets) into one `Version` of one `Document`; a `Suggestion` is a **full replacement** of that one document's content. An OpenSpec *change* is a **set of files** (`proposal.md` + `tasks.md` + `design.md` + N delta `spec.md`), and on archive the delta merges into a **different** canonical file (`openspec/specs/<cap>/spec.md`) that is not itself the thing under comment. Three mismatches:

1. **Grouping.** "One reviewable unit" = a folder, but `Document` is one file.
2. **Promotion target.** Accepting a tracked change edits the delta file; the *value* lands later in a different canonical file at archive time.
3. **Cross-file invariants.** Quality is a property *across* proposal ↔ tasks ↔ delta specs, not within one file.

### 2.3 Concrete outbox-md changes

**Data model — add a `ChangeSet` above `Document` (adapter-owned grouping).**

```
ChangeSet {
  id            string
  adapter       string   // "openspec"
  rootPath      string   // openspec/changes/<change-id>/
  changeId      string   // "add-dark-mode"
  memberDocIds  []string // proposal.md, tasks.md, design.md, specs/*/spec.md as Documents
  status        string   // draft | approved | amending  (mirrors Document lifecycle, set-wide)
}
```

Each member file is still an ordinary `Document` (so commenting, anchoring, suggestions, history all work **unchanged**). The `ChangeSet` is a thin grouping + the unit the UI presents as "one change to review." Flat `.md` is the degenerate adapter: a `ChangeSet` of one document, which is exactly today's behaviour — so the core stays single-file and the adapter is additive.

**Promotion onto the existing approve seam (do not invent a parallel "archive").** The governance cycle already gives us `draft → approved → amending → approved`, where **re-approve is the only thing that writes a baseline to disk**. OpenSpec archive (merge delta → `specs/`, move folder) maps onto **approving the ChangeSet**:

- Approving a ChangeSet pins each member's baseline (existing per-doc approve), then runs an adapter **promotion** step that applies the delta operations to the canonical `openspec/specs/<cap>/spec.md` files and relocates the change folder to `changes/archive/YYYY-MM-DD-<id>/`.
- Promotion is itself **a tracked change to the canonical spec files** that the human accepts — it is *not* a silent server write. The merged canonical content is rendered as a diff against the current `specs/<cap>/spec.md`; the human accepts; only then is disk rewritten and versioned. This keeps "the file is never silently changed" intact even though the bytes move between files.
- Open question (see §6): whether to reuse OpenSpec's own merge or reimplement it in Go (§2.4).

**Delta-aware tracked changes.** When the agent proposes against a delta `spec.md`, the suggestion stays a full-replacement of *that delta file* (no model change). The adapter adds a **delta lens**: it parses the `## ADDED/MODIFIED/REMOVED/RENAMED` sections and renders the human a structure-aware diff ("MODIFIED requirement *Session Expiration* → …") on top of the raw text diff. No new suggestion type; a richer renderer keyed on `adapter == "openspec"`.

**`openspec validate --strict` as a pre-accept gate (config-driven, BYO-safe).** outbox-md is Go and ships nothing — it must not embed OpenSpec or reimplement its evolving rules by default. The gate **shells out to the user's installed `openspec` binary** (preserves zero-embedding/local-first; degrades gracefully when absent). New `outbox.yaml` keys, slotting into the existing config loader next to `batch_size` / `post_approval_comments`:

```yaml
openspec:
  validate: block        # off | warn | block   (default: warn)
  strict: true           # pass --strict
  binary_path: openspec  # resolved on PATH; if missing → degrade to "warn" + surface a notice
```

- `block` — on **accept** of a suggestion against an OpenSpec file (and on ChangeSet approve), run `openspec validate <change-id> --strict --json` against the *proposed* content; a non-empty error set **blocks the accept** and shows the issues inline. This is a real safe-by-construction lever: a spec that fails validation never becomes the baseline.
- `warn` (default) — run it, surface issues, but let the human accept anyway.
- `off` / binary absent — no gate; UI shows "validation unavailable."

The gate validates **proposed** content (a temp materialisation of the would-be accept), never mutating disk. Credentials are never involved — `openspec validate` is offline and deterministic.

**Cross-file consistency checks (a real OpenSpec invariant, not a vague "compare files").** OpenSpec's `proposal.md` **Capabilities** section is, in its own words, *"the contract between proposal and specs phases"*: every capability listed under **New/Modified Capabilities** must have a matching `specs/<name>/spec.md` delta. The adapter computes:

- **Capabilities ↔ deltas**: each listed capability has a delta file; each delta file is listed. Mismatch → seed a machine comment (§4) on `proposal.md`.
- **Tasks ↔ specs**: every `## Requirements`/scenario has at least one `tasks.md` checkbox referencing it (heuristic, name match); orphan tasks and uncovered requirements flagged.
- **Design presence**: cross-cutting/breaking proposals without `design.md` → a soft warning.

These run as a **comment source** (§4), not a blocking gate — they seed outbox comments with `authorIdentity = "consistency-linter"` that the human still resolves.

**MCP / HTTP / UI deltas for §2:**

- **MCP**: `read_doc` gains optional `changeSetId` context (so an agent can pull sibling files of the same change); `list_open_comments` already returns `docPath`, which now disambiguates proposal vs delta. No new agent powers — agents still only propose/reply.
- **HTTP**: `GET /api/changesets`, `GET /api/changesets/{id}` (members + per-member status + validation summary); `POST /api/changesets/{id}/approve` (human-only, server-set identity, mirrors `POST /api/docs/{id}/approve`).
- **UI**: a ChangeSet view (proposal/tasks/design/specs as tabs), a validation badge per member, the delta lens diff, and a single "Approve change" that drives promotion.

---

## 3. AI Council (orchestration shape)

### 3.1 Why a council, and why it stays anti-sycophantic by construction

A single agent has *"a strong built-in pull toward agreement"* (AGENTS.md). The mitigation today is a prompt. The council makes anti-sycophancy **structural**: per comment we fan out to **N agents that propose/critique independently and blind** (no agent sees another's output before submitting), each carrying a distinct **lens**, and one of them is a dedicated **skeptic / red-team** member whose job is to argue the comment is wrong or the edit unnecessary. Disagreement is then a first-class output, not an accident.

Lenses (assignable per member): **correctness**, **completeness**, **ambiguity/testability** (does each requirement have a SHALL/MUST + a 4-hashtag scenario?), **risk**, **simplicity**, **skeptic/red-team**.

### 3.2 The 1:N break and the data model

Today `Suggestion.CommentID` is singular and `GET /api/comments/{id}/suggestion` returns one. A council produces **N candidates per comment** plus a synthesis. New entities (additive; flat single-agent flow = a CandidateSet of one, auto-promoted):

```
CandidateSet {
  id          string
  commentId   string
  state       string   // gathering | synthesized | decided
  quorum      int      // expected member count (from runner config, echoed for display)
}

Candidate {
  id            string
  candidateSet  string
  lens          string   // correctness | completeness | ambiguity | risk | simplicity | skeptic
  verdict       string   // edit | reply | reject-comment   (the member's stance)
  rationale     string
  content       string   // proposed full-replacement IF verdict == edit (else empty)
  agentIdentity string   // which model/runner produced it
}

Synthesis {
  id              string
  candidateSet    string
  agreementScore  float   // 0..1, produced by the chair member
  dissent         string  // the minority position, preserved verbatim
  suggestionId    string  // the synthesized Suggestion offered to the human (if any)
}
```

The **synthesis emits an ordinary `Suggestion`** (or a reply), so the human's accept path is exactly today's. The candidates + dissent + score hang off it for transparency.

### 3.3 Human-only pick / synthesize step (invariant preserved)

Agents still **cannot accept their own work**. Two human-only operations (server-set identity, like resolve/approve):

- **Synthesis is allowed to be machine-produced** (the chair is just another runner step), but it only *proposes*.
- **Pick** — `POST /api/comments/{id}/candidates/{cid}/pick` lets the human choose a specific candidate over the synthesized one (or accept the synthesis). This is the human decision point; there is no MCP tool for it.

### 3.4 New MCP tool — `submit_review` (complements `propose_suggestion`)

Council members do not each call `propose_suggestion` (that would imply N competing baselines). Instead a new tool, **a sibling of `propose_suggestion`**:

```
submit_review(commentId, token, lens, verdict, rationale, content?, agentIdentity)
  verdict ∈ { edit, reply, reject-comment }
  content required iff verdict == edit
```

`submit_review` records a **Candidate** in the comment's CandidateSet; it never writes disk and never resolves anything. `propose_suggestion` remains the single-agent fast path (and is, under the hood, `submit_review` with an auto-promoted set of one). `claim_comment` semantics extend so multiple council members may hold the same comment under distinct agent identities (the batch-size cap still applies per runner).

### 3.5 Driven by the webhook runner — credentials stay OFF-server

This is the BYO tripwire. **The server stores candidates, exposes `submit_review`, and records the decision log. It does not call models, hold keys, run quorum logic, or pick the stronger-model tiebreak.** All of that lives in the **external webhook runner** (the in-flight webhooks cycle: `comment.created` fires → runner fans out to its N models → each calls `submit_review` → chair posts the synthesis). Quorum, stronger-model tiebreak, and chair selection are **runner configuration**, never server config. If a council feature seems to need a key on the server, it belongs on the runner.

### 3.6 Recorded in the decision log

The decision-log already enumerates `Kind ∈ {created, comment, proposal, edit, approval}`. **Extend that enum** — `+ candidate`, `+ synthesis` — rather than adding a parallel log. History then shows: comment → N candidates (with lens + verdict) → synthesis (agreement score + dissent) → human pick → edit → approval. The dissent line is deliberately preserved so an approved spec carries the record of what the skeptic said.

**HTTP / UI deltas for §3:** `GET /api/comments/{id}/candidates` (the set + synthesis); `POST …/candidates/{cid}/pick` (human-only). UI: a "council" panel on a comment showing each lens's verdict/rationale, the agreement score, the dissent callout, and pick controls.

---

## 4. Other quality levers (comment sources + verifiers)

All of these are **new comment sources** or **pre-accept verifiers**. None gives an agent new authority; the human still resolves and accepts.

**4.1 Linters that SEED the outbox.** A deliberate, designed relaxation of today's "agents respond, they don't initiate" — but the initiators are **machines, not the BYO agent**, and their output is still human-resolved. Sources:

- **Validation failures** — `openspec validate` errors/warnings become anchored comments on the offending requirement/line (`authorIdentity = "openspec-validate"`).
- **Ambiguity / untestable / missing-acceptance-criteria heuristics** — requirements lacking SHALL/MUST, requirements with **0 scenarios**, scenarios not in 4-hashtag form, weasel words ("should probably", "etc.", "TBD"). Each seeds a comment (`authorIdentity = "ambiguity-linter"`).

Provenance rides on the **existing** `Comment.authorIdentity` / `Comment.owner` fields — no schema change to carry it; only the set of recognised identities grows. These run on the API server (deterministic, offline, no credentials) or are themselves pushed in by an external linter over a new `POST /api/docs/{id}/comments` with a machine identity.

**4.2 Cross-spec contradiction detection (embeddings).** Find requirements across `openspec/specs/**` that contradict or duplicate each other (e.g. "sessions expire after 30 minutes" vs "60 minutes"). Embedding + nearest-neighbour over requirement blocks, surfaced as comments linking the two locations. **BYO note:** embeddings need a model — so this is a **runner-side** job that posts comments back via the HTTP comment endpoint, *not* a server feature. The server never embeds.

**4.3 Diff-quality verifier before accept.** Before a human accepts, a verifier checks the *proposed* content is a **minimal, faithful** edit: no unrelated sections rewritten, anchor still resolves, no accidental requirement deletion, validation not regressed. Server-side, deterministic checks gate; model-based "is this faithful?" judgement is a runner concern. Verifier output is advisory unless `openspec.validate: block`.

**4.4 Pluggable templates.** Each adapter ships templates (OpenSpec's proposal/spec/tasks/design; RFC; ADR) so a new change scaffolds correctly and linters know the expected section set. Templates live with the adapter; selecting an adapter selects its template + lint set.

**4.5 Eval / golden harness for reviewer agents.** A fixtures set of (document, comment, expected-class-of-response) — including **traps where the correct move is to disagree** — to measure whether an agent (or council) rubber-stamps. Runs offline against recorded candidates; scores sycophancy rate, validation-pass rate, minimality. This is how we keep anti-sycophancy honest as models change.

**4.6 Provenance + confidence in UI.** Surface, per comment/candidate: who raised it (human / which linter / which model), and — for candidates — the council's agreement score and dissent. All read off existing fields (`authorIdentity`) plus the §3 `Synthesis`. No new schema; a UI affordance.

---

## 5. Sequencing

1. **Webhooks + reply-reopen** *(in flight)* — events (`comment.created/replied/resolved`, `document.approved`) so an external runner reacts instead of polling. This is the substrate everything BYO-orchestrated rides on; finish it first.
2. **AI council on the runner** — `submit_review` MCP tool + `CandidateSet`/`Candidate`/`Synthesis` model + human-only pick + decision-log enum extension. Server stores/records; runner orchestrates models. Delivers the marquee anti-sycophancy win on top of (1) with no core change.
3. **OpenSpec adapter** — `ChangeSet` grouping, delta lens, `openspec validate --strict` pre-accept gate, capabilities↔deltas / tasks↔specs consistency comments, promotion-on-approve. The first non-flat format; proves the adapter layer.
4. **Linters / verifier / eval** — ambiguity/untestable heuristics, contradiction detection (runner-side embeddings), diff-quality verifier, golden harness, provenance UI. Quality polish across whichever adapters exist.

Rationale: (1) unblocks (2) and (3); council (2) is pure additive orchestration and the highest-leverage; the OpenSpec adapter (3) is the heaviest (cross-file model + promotion) so it follows once the orchestration contract is proven; (4) is incremental hardening.

---

## 6. Open questions / risks

- **Promotion engine — reuse vs reimplement.** Should ChangeSet-approve shell out to `openspec archive` (authoritative merge, but mutates the folder layout and assumes the binary) or reimplement the delta→canonical merge in Go (no dependency, but we own OpenSpec's evolving merge semantics)? Leaning shell-out (consistent with the validate gate), but archive *moves* the folder, which fights our "tracked change the human accepts" model — needs a dry-run/`--json` mode or a Go merge that produces proposed content without moving anything. **Hardest open item.**
- **REMOVED requirement ambiguity.** Template asks for `**Reason**`/`**Migration**`; validator enforces names only. Our consistency linter should warn-not-block on missing Reason/Migration to match validator behaviour while nudging template intent.
- **opsx migration risk.** OpenSpec is actively moving to the artifact-guided "opsx" workflow. The on-disk artifact shapes are stable today, but the adapter must pin to the artifact format (proposal/specs/design/tasks + delta headers), not to CLI command names, which are in flux.
- **Council latency & cost bounding.** N models per comment multiplies wall-clock and spend. Bounds (all **runner-side**, server agnostic): per-comment member cap, a synthesis timeout that proceeds with whatever candidates arrived (quorum < N tolerated), short-circuit when the skeptic and majority agree, and only fanning out comments the human flags "council" vs the single-agent fast path for routine ones.
- **Validate-as-gate vs local-first/BYO.** Shelling out adds an install dependency (`openspec` on PATH). We degrade to `warn` when absent — but a `block` config with a missing binary must fail safe (never silently pass). Decision: missing binary downgrades `block`→`warn` and surfaces a visible notice, never blocks-by-erroring.
- **Multiple agents on one comment.** Council means several identities `claim_comment` the same comment. The claim/batch model and the no-reaper caveat (crashed claims aren't auto-recovered) get worse with N members — a claim reaper likely becomes a prerequisite for (2).
- **Adapter detection.** How does outbox-md know a folder is an OpenSpec change vs flat `.md`? Proposal: presence of `openspec/` + `changes/<id>/proposal.md`, selectable/overridable in `outbox.yaml`.
