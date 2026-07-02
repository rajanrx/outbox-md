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
export type Project = { name: string; root?: string; docs?: string[] | string; path?: string };
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

export async function listDocs(): Promise<{ id: string; path: string; project: string }[]> {
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
export async function rejectSuggestion(commentId: string): Promise<unknown> {
  const r = await fetch(`/api/comments/${commentId}/reject`, { method: "POST" });
  return r.ok ? r.json().catch(() => null) : null;
}

// AppConfig is the subset of /api/config the UI reads directly. version is "dev"
// for local builds and the release tag otherwise.
export type AppConfig = { version: string };
export async function getConfig(): Promise<AppConfig | null> {
  const r = await fetch("/api/config");
  return r.ok ? r.json() : null;
}

// Settings is the editable subset of a project's outbox.yaml, keyed by the
// outbox.yaml field names (the same keys the PUT endpoint accepts).
export type Settings = { auto_update: boolean; auto_reply: boolean; agent_cmd: string };

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
