# Change: AI Council — server slice

## Motivation

Roadmap §3 ("AI Council") makes anti-sycophancy *structural*: per comment, N agents
review independently and blind, each with a distinct lens (and one dedicated
skeptic), producing **N candidates + a synthesis** instead of a single suggestion.
This change lands the **server slice only**: storage, the `submit_review` MCP tool,
two HTTP endpoints, and the decision-log additions. The orchestration that calls
models, holds keys, runs quorum/tiebreak, and selects the chair lives entirely in
the **external webhook runner** (roadmap §3.5) and ships in a later PR — as does the
web/UI council panel. The server stores candidates and records the decision; it
never calls a model.

## Data model (additive — final field lists)

Three new entities. A flat single-agent flow is unchanged; the council path is added
*alongside* `propose_suggestion`, not on top of it.

```
CandidateSet { id, commentId (unique), state, quorum }
  state ∈ { gathering | synthesized | decided }   // domain consts
  quorum int   // expected member count, echoed from runner config for display (0 = unknown)

Candidate { id, candidateSetId, lens, verdict, rationale, content, agentIdentity, chosen }
  lens    ∈ { correctness | completeness | ambiguity | risk | simplicity | skeptic }
  verdict ∈ { edit | reply | reject_comment }
  content  string   // full replacement, required iff verdict == edit (else empty)
  chosen   bool      // set true by the human-only PickCandidate

Synthesis { id, candidateSetId, agreementScore (0..1), dissent, suggestionId, createdBy }
```

The **synthesis (and a human pick of an edit) emit an ordinary `Suggestion`** via the
existing `CreateSuggestion` path, so the human accept-flow is exactly today's. The
candidates + dissent + agreement score hang off the set for transparency.

## `submit_review` MCP tool

```
submit_review(commentId, token, lens, verdict, rationale, content?, agentIdentity)
  → records a Candidate in the comment's CandidateSet (created lazily, one per comment)
```

- Validates the **claim token** via the existing `requireToken` (the runner claims the
  comment once and shares the token across its N members, distinguished by
  `agentIdentity` — multi-member distinct-token claims are future work, roadmap §6).
- Enforces **content required iff `verdict == edit`** (strict both directions).
- Never writes disk, never resolves, never changes the comment's status — candidates
  accumulate while the set stays `gathering`.
- `propose_suggestion` is unchanged: it remains the single-agent fast path. Unifying it
  with the council path (so a flat flow is "a CandidateSet of one, auto-promoted") is a
  later step, not this PR.

## HTTP endpoints

- `GET /api/comments/{id}/candidates` → `{ set, candidates, synthesis }` JSON (the
  council view for the future UI panel).
- `POST /api/comments/{id}/candidates/{cid}/pick` → **human-only**. Marks the chosen
  candidate, sets the set `state = decided`, and — if the candidate is an edit — emits
  the accept-eligible `Suggestion` the human then accepts through the unchanged accept
  path (it does **not** auto-accept). There is no MCP equivalent, by design.

## Decision-log additions

The decision log is **derived live** from the tables (`internal/store/log.go`), not an
append-only table. So "logging" a candidate/synthesis means `ListDecisionLog` surfaces
the new rows and `kindOrder` is extended. The `Kind` enum gains `candidate` and
`synthesis`; ordering becomes
`created < comment < candidate < synthesis < proposal < edit < approval`
(existing kinds keep their relative order, so the existing log test is unaffected).
A **pick has no dedicated kind** (the enum extension is only `candidate`/`synthesis`):
it is recorded by the candidate's `chosen` flag and the `proposal` entry its emitted
Suggestion produces.

## Invariants preserved

- Agents may **submit reviews/candidates** but **cannot pick, accept, resolve, or
  approve** — those stay human-only with no MCP tool.
- Synthesis and pick emit an ordinary `Suggestion`; the human accepts it via the
  unchanged accept flow. **No file is written except through the existing accept path.**
- The server **stores candidates only** — it does not call models, hold keys, or run
  quorum/tiebreak/chair logic. Those are the future runner.

## Deviations from the literal spec (store idiom forced these)

- **No append API for the log.** The decision log is derived, so candidate/synthesis
  "log entries" are produced by extending `ListDecisionLog`, not by writing a log row.
- **Verdict spelling** follows this change's `reject_comment` (underscore), not the
  roadmap prose's `reject-comment` (hyphen) — it is a Go/JSON identifier.
- `RecordSynthesis` is a **service method with no transport in this PR** (the only tool
  is `submit_review`; the only endpoints are GET candidates + POST pick). The chair's
  invocation path ships with the runner PR; here it is implemented and unit-tested
  directly so the server-side record + the human-facing Suggestion exist.

## Task list

1. domain: `CandidateSet`/`Candidate`/`Synthesis` + lens/verdict/state consts; extend
   the `LogEntry.Kind` doc comment.
2. store: `candidate_sets`/`candidates`/`syntheses` tables (schema.sql) + CRUD
   (`GetOrCreateCandidateSet`, `AddCandidate`, `ListCandidatesByComment`,
   `RecordSynthesis`, `GetSynthesisByComment`, `MarkCandidateChosen`,
   `SetCandidateSetState`); extend `ListDecisionLog` + `kindOrder`.
3. service: `SubmitReview`, `ListCandidates`, `RecordSynthesis`, `PickCandidate`
   (human-only).
4. mcp: register `submit_review`; update the `process_outbox` prompt + AGENTS.md table.
5. api: `GET …/candidates`, `POST …/candidates/{cid}/pick`.
6. tests: store round-trip, service (token/validation/human-only/shape), mcp
   registration + drive, api GET/POST.
