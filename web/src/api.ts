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
  document: { id: string; path: string; status: "draft" | "approved" | "amending"; approvedVersionId: string };
  content: string;
  comments: Comment[];
  baselineContent: string;
};
export type Suggestion = { id: string; proposedContent: string; state: string };

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

export async function listDocs(): Promise<{ id: string; path: string }[]> {
  return (await fetch("/api/docs")).json();
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
