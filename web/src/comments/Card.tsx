import { useEffect, useRef, useState } from "react";
import { getThread, reply, resolve, type Comment, type ThreadMessage } from "../api";
import { DiffPanel } from "../suggestion/DiffPanel";

const LocateIcon = () => (
  <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.4">
    <circle cx="8" cy="8" r="3.2" />
    <path d="M8 1.4v2M8 12.6v2M1.4 8h2M12.6 8h2" strokeLinecap="round" />
  </svg>
);
const PinIcon = ({ filled }: { filled: boolean }) => (
  <svg viewBox="0 0 16 16" fill={filled ? "currentColor" : "none"} stroke="currentColor" strokeWidth="1.3">
    <path d="M6 1.8h4l-.6 3.2 2 2.3v1.3H4.6V7.3l2-2.3L6 1.8z" strokeLinejoin="round" />
    <path d="M8 10.9V14" strokeLinecap="round" />
  </svg>
);

const initial = (who: string) => (who?.[0] || "?").toUpperCase();

export function Card({ comment, currentContent, active = false, pinned = false, offscreen = false, reloadKey = 0, onActivate, onJump, onTogglePin, onChange }: {
  comment: Comment;
  currentContent: string;
  active?: boolean;
  pinned?: boolean;
  offscreen?: boolean;
  // Bumped by the parent on each SSE event for the open doc; the thread state is
  // local to this Card and would otherwise only load on comment.id change, so an
  // agent reply/suggestion wouldn't appear in the open thread until a refresh.
  reloadKey?: number;
  onActivate?: () => void;
  onJump?: () => void;
  onTogglePin?: () => void;
  onChange: () => void;
}) {
  const [thread, setThread] = useState<ThreadMessage[]>([]);
  const [draft, setDraft] = useState("");
  const ref = useRef<HTMLDivElement>(null);
  // "AI processing…" is a self-expiring hint: show it only while processingUntil
  // is in the future, and hide it on its own at the deadline (a setTimeout) so the
  // badge clears even if no event arrives. A heartbeat that extends the deadline
  // updates processingUntil, re-running this effect and resetting the timer.
  const [processing, setProcessing] = useState(false);
  useEffect(() => {
    const until = comment.processingUntil ? Date.parse(comment.processingUntil) : NaN;
    const ms = until - Date.now();
    if (!Number.isFinite(until) || ms <= 0) { setProcessing(false); return; }
    setProcessing(true);
    const t = setTimeout(() => setProcessing(false), ms);
    return () => clearTimeout(t);
  }, [comment.processingUntil]);
  const load = () => getThread(comment.id).then((t) => setThread(t ?? []));
  useEffect(() => { load(); /* eslint-disable-next-line */ }, [comment.id, reloadKey]);
  useEffect(() => {
    if (active) ref.current?.scrollIntoView({ block: "nearest", behavior: "smooth" });
  }, [active]);

  const stop = (e: React.MouseEvent, fn?: () => void) => { e.stopPropagation(); fn?.(); };
  const sendReply = async () => { if (!draft.trim()) return; await reply(comment.id, draft); setDraft(""); await load(); };

  return (
    <div ref={ref} className={"card" + (active ? " active" : "") + (pinned ? " pinned" : "")} data-comment={comment.id} onClick={onActivate}>
      <div className="card-bar">
        <span className={`status-tag status-${comment.status}`}>{comment.status}</span>
        {comment.postApproval && (
          <span className="post-approval-tag" title="Feedback added after approval">post-approval</span>
        )}
        {processing && (
          <span className="processing-tag" title="An AI agent is working on this comment">⋯ AI processing</span>
        )}
        <span className="card-tools">
          {offscreen && (
            <button className="ic-btn" title="Scroll to text" aria-label="Scroll to text" onClick={(e) => stop(e, onJump)}><LocateIcon /></button>
          )}
          <button className={"ic-btn" + (pinned ? " on" : "")} title={pinned ? "Unpin" : "Pin"} aria-label={pinned ? "Unpin" : "Pin"} onClick={(e) => stop(e, onTogglePin)}><PinIcon filled={pinned} /></button>
        </span>
      </div>

      <div className="posts">
        {thread.length === 0 && <div className="post-empty">No comment text yet.</div>}
        {thread.map((m) => (
          <div key={m.id} className="post">
            <span className={`avatar who-${m.authorIdentity}`}>{initial(m.authorIdentity)}</span>
            <div className="post-main">
              <div className="post-author">{m.authorIdentity}</div>
              <div className="post-text">{m.body}</div>
            </div>
          </div>
        ))}
      </div>

      {comment.status === "addressed" && (
        <DiffPanel commentId={comment.id} currentContent={currentContent} onDone={onChange} />
      )}

      {comment.status !== "resolved" && (
        <div className="card-actions" onClick={(e) => e.stopPropagation()}>
          <input
            value={draft} placeholder="Reply…"
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => { if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); sendReply(); } }}
          />
          <button className="link-btn" disabled={!draft.trim()} onClick={sendReply}>Reply</button>
          <button className="link-btn resolve" onClick={async () => { await resolve(comment.id); onChange(); }}>Resolve</button>
        </div>
      )}
    </div>
  );
}
