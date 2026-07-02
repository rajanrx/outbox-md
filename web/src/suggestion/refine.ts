// Refine loop model: GitHub-style inline comments on a suggestion diff, formatted
// into ONE structured human reply that is posted through the existing reply path
// (→ comment.replied → the auto-reply agent proposes an improved suggestion). No
// new backend plumbing — a Refine is just a well-formatted human reply.

// Which side of the diff a line comment is anchored to. "new" = the proposed
// (after) text; "old" = a line the suggestion removes (before-only).
export type DraftSide = "old" | "new";

// LineRef identifies the diff line a comment attaches to. lineNo is the 1-based
// number shown in that side's gutter; snippet is the line's text, captured at
// draft time so the agent can locate it even after the suggestion changes.
export type LineRef = { side: DraftSide; lineNo: number; snippet: string };

// LineDraft is an unsent inline comment held in component state (NOT posted until
// the user clicks Refine). id is a client-only key for edit/remove.
export type LineDraft = LineRef & { id: string; body: string };

// lineKey is the stable per-line map key. It is reproducible in both the split
// and inline views for insert/delete/replace lines; equal (context) lines are
// only commentable from the old-side gutter, so their key never diverges.
export function lineKey(side: DraftSide, lineNo: number): string {
  return `${side}:${lineNo}`;
}

// groupDrafts buckets drafts by their line key for O(1) per-row lookup while
// rendering the diff. Insertion order within a bucket is preserved.
export function groupDrafts(drafts: LineDraft[]): Map<string, LineDraft[]> {
  const m = new Map<string, LineDraft[]>();
  for (const d of drafts) {
    const k = lineKey(d.side, d.lineNo);
    const list = m.get(k);
    if (list) list.push(d);
    else m.set(k, [d]);
  }
  return m;
}

// canRefine gates the "Refine (N)" button: enabled only once at least one inline
// comment has been drafted.
export function canRefine(drafts: LineDraft[]): boolean {
  return drafts.some((d) => d.body.trim().length > 0);
}

const SNIPPET_MAX = 48;

// clampSnippet collapses a line's whitespace to single spaces and truncates it so
// the formatted message stays compact while still quoting enough for the agent to
// locate the line.
export function clampSnippet(text: string, max = SNIPPET_MAX): string {
  const s = text.replace(/\s+/g, " ").trim();
  return s.length > max ? s.slice(0, max - 1).trimEnd() + "…" : s;
}

// formatRefineMessage renders the drafts into ONE structured human reply. Drafts
// with an empty body are dropped; the rest are ordered by line number (old before
// new on a tie) so the feedback reads top-to-bottom. Old-side (removed) lines are
// annotated so the agent knows the note is about text the suggestion deletes.
//
//   Refine — inline feedback on your suggestion:
//   - line 12 «the quick brown fox»: too informal, use plain language
//   - line 18 (removed) «drop me»: drop this sentence
export function formatRefineMessage(drafts: LineDraft[]): string {
  const items = drafts
    .filter((d) => d.body.trim().length > 0)
    .slice()
    .sort((a, b) => a.lineNo - b.lineNo || (a.side === b.side ? 0 : a.side === "old" ? -1 : 1));
  if (items.length === 0) return "";
  const lines = items.map((d) => {
    const where = d.side === "old" ? `line ${d.lineNo} (removed)` : `line ${d.lineNo}`;
    return `- ${where} «${clampSnippet(d.snippet)}»: ${d.body.trim()}`;
  });
  return ["Refine — inline feedback on your suggestion:", ...lines].join("\n");
}
