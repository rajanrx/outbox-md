import type { Comment } from "../api";

export function Outbox({ comments, onSelect }: {
  comments: Comment[] | null;
  onSelect: (c: Comment) => void;
}) {
  const list = comments ?? [];
  return (
    <div>
      <h3>Outbox ({list.length})</h3>
      <ul>
        {list.map((c) => (
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
