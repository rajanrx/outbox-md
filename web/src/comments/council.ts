// Pure helpers over a CouncilView: derive the "discussing" state, member counts,
// per-round grouping for the transcript, the consensus verdict, and the deadlock
// split. Kept side-effect-free so the Card, the modal, and unit tests share one
// source of truth for these derivations.
import type { Candidate, CouncilView, DiscussionMessage } from "../api";

// distinctMembers counts the unique participating identities across candidates —
// the "members: N" hint. Falls back to the set's quorum when no candidate has
// landed yet (0 = unknown).
export function distinctMembers(view: CouncilView): number {
  const ids = new Set(view.candidates.map((c) => c.agentIdentity).filter(Boolean));
  return ids.size || view.set.quorum || 0;
}

// maxRound is the highest round seen across candidates AND discussion messages.
export function maxRound(view: CouncilView): number {
  let m = 0;
  for (const c of view.candidates) m = Math.max(m, c.round);
  for (const d of view.discussion) m = Math.max(m, d.round);
  return m;
}

// isDiscussing derives the debate state from the DATA, not set.state (which the
// backend keeps at "gathering" through discussion): true once any candidate has
// been revised in a round > 0.
export function isDiscussing(view: CouncilView): boolean {
  return view.candidates.some((c) => c.round > 0);
}

// isDecided is true once a candidate has been picked (chosen) or the set reached
// its terminal decided state. After a decision the deadlock pick cards give way to
// the ordinary suggestion/Accept flow.
export function isDecided(view: CouncilView): boolean {
  return view.set.state === "decided" || view.candidates.some((c) => c.chosen);
}

// isDeadlock is the "no consensus" condition: the chair recorded a synthesis but
// its confidence is below the configured deadlock threshold — AND no pick has been
// made yet (a strict `<` so confidence == threshold is the normal/consensus case).
export function isDeadlock(view: CouncilView, threshold: number): boolean {
  return !!view.synthesis && view.synthesis.confidence < threshold && !isDecided(view);
}

// severity buckets the chair confidence against the same threshold used for the
// deadlock split: "low" (amber) exactly when the verdict is no-consensus, "high"
// (quiet/green) otherwise.
export function severity(confidence: number, threshold: number): "high" | "low" {
  return confidence < threshold ? "low" : "high";
}

// finalPositions returns each member's LATEST candidate (highest round), in the
// members' first-seen order — the competing "no consensus" options ranked for the
// deadlock pick cards, and the basis for the plurality verdict.
export function finalPositions(view: CouncilView): Candidate[] {
  const byMember = new Map<string, Candidate>();
  for (const c of view.candidates) {
    const prev = byMember.get(c.agentIdentity);
    if (!prev || c.round >= prev.round) byMember.set(c.agentIdentity, c);
  }
  return [...byMember.values()];
}

// chosenVerdict is the consensus stance to measure dissent against: the picked
// candidate's verdict if any, else the plurality verdict among the final positions.
export function chosenVerdict(view: CouncilView): string {
  const picked = view.candidates.find((c) => c.chosen);
  if (picked) return picked.verdict;
  const tally = new Map<string, number>();
  for (const c of finalPositions(view)) tally.set(c.verdict, (tally.get(c.verdict) ?? 0) + 1);
  let best = "";
  let n = -1;
  for (const [v, count] of tally) if (count > n) { best = v; n = count; }
  return best;
}

// A single round bucket for the transcript: the members' takes (candidates) and
// the debate messages posted in that round.
export type RoundGroup = { round: number; candidates: Candidate[]; messages: DiscussionMessage[] };

// groupByRound buckets candidates + discussion messages by round, ascending.
// Round 0 holds the independent blind takes; round >=1 holds discussion.
export function groupByRound(view: CouncilView): RoundGroup[] {
  const rounds = new Set<number>();
  for (const c of view.candidates) rounds.add(c.round);
  for (const d of view.discussion) rounds.add(d.round);
  return [...rounds]
    .sort((a, b) => a - b)
    .map((round) => ({
      round,
      candidates: view.candidates.filter((c) => c.round === round),
      messages: view.discussion.filter((d) => d.round === round),
    }));
}

// authorOf resolves a "member" ref target id to that member's identity for a
// human-readable citation ("responding to <who>"), searching candidates then
// discussion messages. Returns a shortened id when the referent isn't in view.
export function authorOf(view: CouncilView, targetId: string): string {
  const c = view.candidates.find((x) => x.id === targetId);
  if (c) return c.agentIdentity;
  const d = view.discussion.find((x) => x.id === targetId);
  if (d) return d.agentIdentity;
  return targetId.length > 8 ? `member-${targetId.slice(0, 6)}` : `member-${targetId}`;
}
