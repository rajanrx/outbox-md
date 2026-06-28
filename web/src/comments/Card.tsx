import { useEffect, useState } from "react";
import { getThread, reply, resolve, type Comment, type ThreadMessage } from "../api";
import { DiffPanel } from "../suggestion/DiffPanel";

export function Card({ comment, currentContent, onChange }: {
  comment: Comment;
  currentContent: string;
  onChange: () => void;
}) {
  const [thread, setThread] = useState<ThreadMessage[]>([]);
  const [draft, setDraft] = useState("");
  const [showDiff, setShowDiff] = useState(false);
  const load = () => getThread(comment.id).then((t) => setThread(t ?? []));
  useEffect(() => { load(); /* eslint-disable-next-line */ }, [comment.id]);

  return (
    <div className="card" data-comment={comment.id}>
      <div className="card-head">
        <span className={`who who-${comment.authorIdentity}`}>{comment.authorIdentity}</span>
        <span className={`status status-${comment.status}`}>{comment.status}</span>
      </div>
      {thread.map((m) => (
        <div key={m.id} className="msg"><b className={`who-${m.authorIdentity}`}>{m.authorIdentity}:</b> {m.body}</div>
      ))}
      {comment.status === "addressed" && (
        <button className="review-btn" onClick={() => setShowDiff(true)}>Review suggestion</button>
      )}
      {comment.status !== "resolved" && (
        <div className="card-actions">
          <input value={draft} placeholder="Reply…" onChange={(e) => setDraft(e.target.value)} />
          <button disabled={!draft.trim()} onClick={async () => { await reply(comment.id, draft); setDraft(""); await load(); }}>Reply</button>
          <button className="btn-primary" onClick={async () => { await resolve(comment.id); onChange(); }}>Resolve</button>
        </div>
      )}
      {showDiff && <DiffPanel commentId={comment.id} currentContent={currentContent} onDone={() => { setShowDiff(false); onChange(); }} />}
    </div>
  );
}
