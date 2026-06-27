import type { Comment } from "../api";

export function Outbox({ comments, onSelect }: {
  comments: Comment[];
  onSelect: (c: Comment) => void;
}) {
  return (
    <div>
      <h3>Outbox ({comments.length})</h3>
      <ul>
        {comments.map((c) => (
          <li key={c.id}>
            <button onClick={() => onSelect(c)}>
              [{c.status}] {c.anchor.start}–{c.anchor.end} · {c.authorIdentity}
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}
