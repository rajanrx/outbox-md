import type { Anchor } from "./lib/selection";
export type { Anchor };

export type Comment = {
  id: string;
  anchor: Anchor;
  status: string;
  authorIdentity: string;
};
export type DocView = {
  document: { id: string; path: string };
  content: string;
  comments: Comment[];
};
export type Suggestion = { id: string; proposedContent: string; state: string };

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
  return (await fetch(`/api/comments/${commentId}/accept`, { method: "POST" })).json();
}

export type ThreadMessage = { id: string; authorIdentity: string; body: string };

export async function getThread(commentId: string): Promise<ThreadMessage[]> {
  return (await fetch(`/api/comments/${commentId}/thread`)).json();
}
export async function reply(commentId: string, body: string): Promise<unknown> {
  return (await fetch(`/api/comments/${commentId}/reply`, { method: "POST", body: JSON.stringify({ body }) })).json();
}
export async function resolve(commentId: string): Promise<unknown> {
  return (await fetch(`/api/comments/${commentId}/resolve`, { method: "POST" })).json();
}
export async function rejectSuggestion(commentId: string): Promise<unknown> {
  return (await fetch(`/api/comments/${commentId}/reject`, { method: "POST" })).json();
}
