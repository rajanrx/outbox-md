// RetrievalChip surfaces the KB retrieval mode (design §5.5) as a header chip in
// the same family as the version badge. It makes a fallback to keyword search
// VISIBLE rather than silent:
//   - semantic     → the healthy default; render nothing (no chrome noise).
//   - keyword-only → INFO chip: no embedder configured; FTS5 is serving.
//   - degraded     → WARNING chip: an embedder IS configured but is failing.
// Any other/empty value (retrieval unwired) renders nothing.

type Props = { mode: string | undefined };

const COPY: Record<string, { label: string; title: string; variant: "info" | "warn" }> = {
  "keyword-only": {
    label: "Keyword search (FTS5)",
    title: "Keyword search (FTS5) — install ollama for semantic recall",
    variant: "info",
  },
  degraded: {
    label: "Retrieval degraded",
    title: "Embedder unavailable — retrieval degraded to keyword search",
    variant: "warn",
  },
};

export function RetrievalChip({ mode }: Props) {
  if (!mode) return null;
  const c = COPY[mode];
  if (!c) return null; // semantic (or unknown) → quiet
  return (
    <span
      className={`retrieval-chip ${c.variant}`}
      role="status"
      title={c.title}
      data-mode={mode}
    >
      {c.label}
    </span>
  );
}
