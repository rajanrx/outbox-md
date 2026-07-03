export type Anchor = { start: number; end: number };

export type Comment = {
  id: string;
  anchor: Anchor;
  status: string;
  authorIdentity: string;
  postApproval: boolean;
  // Ephemeral, self-expiring hint that an AI agent is working this comment.
  // Absent/empty when idle; an ISO timestamp in the future while processing.
  processingUntil?: string;
  // Per-comment council flag: true ⇒ the full council processes this comment,
  // false ⇒ a single agent (the chair command). Seeded from council_default at
  // creation and toggled from the thread UI. Always present on the current server.
  councilEnabled?: boolean;
};
export type DocView = {
  document: { id: string; project: string; path: string; status: "draft" | "approved" | "amending"; approvedVersionId: string };
  content: string;
  comments: Comment[];
  baselineContent: string;
};

// Project is a registered project outbox-md serves: a repo root plus a LIST of
// docs subpaths (root/<docs[i]> are the served spec dirs; the project serves
// their union). name is the root's basename. In single-folder mode the server
// returns a single project with an empty name. docs is an array in the current
// server; a bare string (older single-docs server) and path (oldest server) stay
// accepted for back-compat.
// Member is one council participant (or the chair): agent is a preset name
// (claude/codex/copilot) OR a raw spawn command; model is an optional free-text
// override. This mirrors the backend's memberDTO {agent, model}.
export type Member = { agent: string; model?: string };

export type Project = {
  name: string;
  root?: string;
  docs?: string[] | string;
  path?: string;
  // The structured council roster the Settings page edits. Present on the current
  // (registry-backed) server; absent on older servers / single-folder mode.
  members?: Member[];
  chair?: Member | null;
};
export type Suggestion = {
  id: string;
  proposedContent: string;
  state: string;
  // The content of the version the suggestion was proposed AGAINST. Used to
  // render a read-only historical diff (againstContent → proposedContent) once a
  // suggestion is accepted/rejected — post-accept the current content already
  // equals proposedContent, so a current-vs-proposed diff would show nothing.
  againstContent?: string;
};

// Row is the shape of one diff line, shared by the client-built single-file
// diff (suggestion/diff.ts) and the folder view (built from pending suggestions).
export type { Row } from "./suggestion/diff";

// PendingSuggestion is one doc across the project that currently has a pending
// (proposed) suggestion: its current content and the proposed replacement, so
// the UI can render the same current-vs-proposed diff shown inline.
export type PendingSuggestion = {
  docId: string;
  path: string;
  commentId: string;
  current: string;
  proposed: string;
};

// getPendingSuggestions returns every pending suggestion across the project,
// used to build the "Folder changes" view from outbox-md's own data (no git).
export async function getPendingSuggestions(): Promise<PendingSuggestion[]> {
  const r = await fetch("/api/suggestions/pending");
  return r.ok ? r.json() : [];
}

export type LogEntry = {
  time: string;
  kind: "created" | "comment" | "proposal" | "edit" | "approval";
  actor: string;
  detail: string;
  version: number;
  reApproval: boolean;
};

export async function getLog(id: string): Promise<LogEntry[]> {
  return (await fetch(`/api/docs/${id}/log`)).json();
}

export async function approve(id: string, note = ""): Promise<unknown> {
  const r = await fetch(`/api/docs/${id}/approve`, { method: "POST", body: JSON.stringify({ note }) });
  return r.ok ? r.json() : null;
}
export async function reapprove(id: string, note = ""): Promise<unknown> {
  const r = await fetch(`/api/docs/${id}/reapprove`, { method: "POST", body: JSON.stringify({ note }) });
  return r.ok ? r.json() : null;
}

export async function listDocs(): Promise<{ id: string; path: string; project: string; openComments?: number }[]> {
  return (await fetch("/api/docs")).json();
}
export async function listProjects(): Promise<Project[]> {
  const r = await fetch("/api/projects");
  return r.ok ? r.json() : [];
}
export async function getDoc(id: string): Promise<DocView> {
  return (await fetch(`/api/docs/${id}`)).json();
}
export async function postComment(id: string, a: Anchor): Promise<Comment> {
  return (await fetch(`/api/docs/${id}/comments`, { method: "POST", body: JSON.stringify(a) })).json();
}
export async function getSuggestion(commentId: string): Promise<Suggestion | null> {
  const r = await fetch(`/api/comments/${commentId}/suggestion`);
  return r.ok ? r.json() : null;
}
export async function accept(commentId: string): Promise<unknown> {
  const r = await fetch(`/api/comments/${commentId}/accept`, { method: "POST" });
  return r.ok ? r.json().catch(() => null) : null;
}

export type ThreadMessage = { id: string; authorIdentity: string; body: string };

export async function getThread(commentId: string): Promise<ThreadMessage[]> {
  return (await fetch(`/api/comments/${commentId}/thread`)).json();
}
export async function reply(commentId: string, body: string): Promise<unknown> {
  const r = await fetch(`/api/comments/${commentId}/reply`, { method: "POST", body: JSON.stringify({ body }) });
  // Throw on non-OK so callers never mistake a 404/500 for success — which would
  // silently drop the user's reply / refine feedback. (r.ok + empty body is fine.)
  if (!r.ok) throw new Error(`reply failed (HTTP ${r.status})`);
  return r.json().catch(() => null);
}
export async function resolve(commentId: string): Promise<unknown> {
  const r = await fetch(`/api/comments/${commentId}/resolve`, { method: "POST" });
  return r.ok ? r.json().catch(() => null) : null;
}
// setCommentCouncil flips a comment's per-comment council on/off flag (human-only,
// like resolve). Throws on a non-OK POST so the caller can keep local state.
export async function setCommentCouncil(commentId: string, enabled: boolean): Promise<void> {
  const r = await fetch(`/api/comments/${commentId}/council`, { method: "POST", body: JSON.stringify({ enabled }) });
  if (!r.ok) throw new Error(`council toggle failed (${r.status})`);
}

export async function rejectSuggestion(commentId: string): Promise<unknown> {
  const r = await fetch(`/api/comments/${commentId}/reject`, { method: "POST" });
  return r.ok ? r.json().catch(() => null) : null;
}

// ── Council read model ──────────────────────────────────────────────
// The council is a per-comment panel of AI members that each review a comment,
// optionally debate over rounds, and are rolled up by a chair into a synthesis.
// These mirror the backend DTOs (internal/domain, internal/service.CouncilView).

// DiscussionRef is a citation on a discussion message: "member" points at another
// member's candidate/message; "kb" points at a knowledge-base passage.
export type DiscussionRef = { kind: "member" | "kb"; targetId: string };

// Candidate is one member's review. round 0 = the independent blind take; round
// >=1 = a revised position submitted during a discussion round. content is a full
// replacement iff verdict === "edit". chosen is set by the human-only pick.
export type Candidate = {
  id: string;
  candidateSetId: string;
  round: number;
  lens: string;
  verdict: "edit" | "reply" | "reject_comment";
  rationale: string;
  content: string;
  agentIdentity: string;
  chosen: boolean;
};

// DiscussionMessage is one attributed message on the debate transcript.
export type DiscussionMessage = {
  id: string;
  candidateSetId: string;
  round: number;
  agentIdentity: string;
  body: string;
  refs: DiscussionRef[];
};

// Synthesis is the chair's roll-up: agreementScore (0..1), confidence (0..100),
// the preserved minority position, and the id of the ordinary Suggestion offered.
export type Synthesis = {
  id: string;
  candidateSetId: string;
  agreementScore: number;
  confidence: number;
  dissent: string;
  suggestionId: string;
  createdBy: string;
};

// CandidateSet stays "gathering" through discussion rounds (a backend constraint),
// so DERIVE "discussing" from candidates having round > 0, not from state.
export type CandidateSet = { id: string; commentId: string; state: string; quorum: number };

// CouncilView is the whole read model for a comment's council.
export type CouncilView = {
  set: CandidateSet;
  candidates: Candidate[];
  discussion: DiscussionMessage[];
  synthesis: Synthesis | null;
};

// getCandidates returns a comment's council view, or null when the comment has no
// candidate set (a plain, non-council comment — the endpoint 404s). Any other
// non-OK response also yields null so a transient error never breaks the plain
// comment UI; the council surface simply stays hidden.
export async function getCandidates(commentId: string): Promise<CouncilView | null> {
  const r = await fetch(`/api/comments/${commentId}/candidates`);
  return r.ok ? r.json() : null;
}

// pickCandidate records the human's pick of one candidate. The server emits the
// ordinary Suggestion and marks the set decided. Throws on a non-OK response —
// surfacing the server's {"error"} message — so a failed pick is never mistaken
// for success and the caller can keep the pick cards on screen.
export async function pickCandidate(commentId: string, candidateId: string): Promise<void> {
  const r = await fetch(`/api/comments/${commentId}/candidates/${candidateId}/pick`, { method: "POST" });
  if (!r.ok) {
    const msg = await r.json().then((j) => j?.error).catch(() => null);
    throw new Error(msg || `pick failed (HTTP ${r.status})`);
  }
}

// AppConfig is the subset of /api/config the UI reads directly. version is "dev"
// for local builds and the release tag otherwise. councilDeadlockThreshold is the
// confidence (0..100) below which a chair verdict is treated as "no consensus".
// retrievalMode is the KB retrieval posture (§5.5): "semantic" | "keyword-only" |
// "degraded", or ""/absent when retrieval is not wired — the header chip reads it.
// councilDefault seeds a new comment's council toggle (system default false).
export type AppConfig = { version: string; councilDeadlockThreshold?: number; retrievalMode?: string; councilDefault?: boolean };
export async function getConfig(): Promise<AppConfig | null> {
  const r = await fetch("/api/config");
  return r.ok ? r.json() : null;
}

// ApiError carries the HTTP status alongside the server's message so callers can
// branch on it (e.g. 409 ⇒ single-folder mode has no projects registry to write).
export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
    this.name = "ApiError";
  }
}

// throwApiError extracts the server's {"error": "..."} body (falling back to the
// status) and throws an ApiError — so every write surfaces the exact server
// message and its status verbatim, never a swallowed failure.
async function throwApiError(r: Response): Promise<never> {
  const msg = await r.json().then((j) => j?.error).catch(() => null);
  throw new ApiError(r.status, msg || `request failed (${r.status})`);
}

// ProjectInput is the body POST /api/projects (and, partially, PATCH) accepts:
// a root dir, its docs subpaths, the member roster, and an optional chair.
export type ProjectInput = { root: string; docs: string[]; members: Member[]; chair?: Member | null };

// createProject registers a new project. Throws ApiError with the server message
// on validation failure (400) or when the server is single-folder mode (409).
export async function createProject(input: ProjectInput): Promise<Project> {
  const r = await fetch("/api/projects", { method: "POST", body: JSON.stringify(input) });
  if (!r.ok) await throwApiError(r);
  return r.json();
}

// updateProject merge-edits a project's docs/members/chair (name + root are
// immutable). Only the keys present in the patch change server-side.
export async function updateProject(name: string, patch: Partial<ProjectInput>): Promise<Project> {
  const r = await fetch(`/api/projects/${encodeURIComponent(name)}`, { method: "PATCH", body: JSON.stringify(patch) });
  if (!r.ok) await throwApiError(r);
  return r.json();
}

// deleteProject unregisters a project. Throws ApiError on 404/409.
export async function deleteProject(name: string): Promise<void> {
  const r = await fetch(`/api/projects/${encodeURIComponent(name)}`, { method: "DELETE" });
  if (!r.ok) await throwApiError(r);
}

// isSingleFolderMode detects the server's single-folder mode from the project
// list: it returns exactly one project with an empty name (registry mode always
// names a project after its root basename). Used to show the "manage via CLI"
// notice instead of a projects form. The 409 on writes is the authoritative
// fallback, handled by the CRUD callers above.
export function isSingleFolderMode(projects: Project[]): boolean {
  return projects.length === 1 && projects[0].name === "";
}

// Settings is the editable subset of a project's outbox.yaml, keyed by the
// outbox.yaml field names (the same keys the PUT endpoint accepts). The council
// guardrails are integers; auto_* are booleans; agent_cmd is a template string.
export type Settings = {
  auto_update: boolean;
  auto_reply: boolean;
  agent_cmd: string;
  council_rounds: number;
  council_budget: number;
  council_deadlock_threshold: number;
  // Seed a new comment's council flag on (full council) rather than single-agent.
  council_default: boolean;
};

// getSettings reads the current project's editable outbox.yaml fields. In
// multi-project mode pass the selected project name; single-folder mode ignores it.
export async function getSettings(project = ""): Promise<Settings | null> {
  const q = project ? `?project=${encodeURIComponent(project)}` : "";
  const r = await fetch(`/api/settings${q}`);
  return r.ok ? r.json() : null;
}

// putSettings writes the given editable fields to the project's outbox.yaml,
// preserving comments + unmanaged keys. Throws with the server message on error.
export async function putSettings(values: Partial<Settings>, project = ""): Promise<Settings> {
  const q = project ? `?project=${encodeURIComponent(project)}` : "";
  const r = await fetch(`/api/settings${q}`, { method: "PUT", body: JSON.stringify(values) });
  if (!r.ok) {
    const msg = await r.json().then((j) => j?.error).catch(() => null);
    throw new Error(msg || `save failed (${r.status})`);
  }
  return r.json();
}
