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

// Project is a registered folder outbox-md serves. In single-folder mode the
// server returns a single project with an empty name.
export type Project = { name: string; path: string };
export type Suggestion = { id: string; proposedContent: string; state: string };

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
  return r.ok ? r.json().catch(() => null) : null;
}
export async function resolve(commentId: string): Promise<unknown> {
  const r = await fetch(`/api/comments/${commentId}/resolve`, { method: "POST" });
  return r.ok ? r.json().catch(() => null) : null;
}
export async function rejectSuggestion(commentId: string): Promise<unknown> {
  const r = await fetch(`/api/comments/${commentId}/reject`, { method: "POST" });
  return r.ok ? r.json().catch(() => null) : null;
}
