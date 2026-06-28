import { useEffect, useState } from "react";
import { computeAnchor } from "../anchor/anchor";
import { rangeForAnchor, commentAtPoint } from "../anchor/highlight";
import { postComment, type Comment } from "../api";
import { Card } from "./Card";
import "./comments.css";

const CSSh = CSS as any;
const canHighlight = () => "Highlight" in window && CSSh.highlights;

function paintHighlights(root: HTMLElement, source: string, comments: Comment[], activeId: string | null) {
  if (!canHighlight()) return;
  const ranges: Range[] = [];
  let activeRange: Range | null = null;
  for (const c of comments) {
    if (c.status === "resolved") continue;
    const r = rangeForAnchor(root, source, c.anchor);
    if (!r) continue;
    if (c.id === activeId) activeRange = r;
    else ranges.push(r);
  }
  CSSh.highlights.set("comment", new (window as any).Highlight(...ranges));
  if (activeRange) CSSh.highlights.set("comment-active", new (window as any).Highlight(activeRange));
  else CSSh.highlights.delete("comment-active");
}

// Scroll the reading pane so a comment's anchored text is comfortably in view.
function scrollReaderToAnchor(root: HTMLElement, source: string, anchor: { start: number; end: number }) {
  const r = rangeForAnchor(root, source, anchor);
  if (!r) return;
  const pane = root.closest(".reader-pane") as HTMLElement | null;
  if (!pane) return;
  const rect = r.getBoundingClientRect();
  const paneRect = pane.getBoundingClientRect();
  pane.scrollTo({ top: pane.scrollTop + (rect.top - paneRect.top) - 140, behavior: "smooth" });
}

export function Margin({ docId, content, rootRef, comments, onChange }: {
  docId: string;
  content: string;
  rootRef: React.RefObject<HTMLDivElement | null>;
  comments: Comment[];
  onChange: () => void;
}) {
  const [pending, setPending] = useState<{ start: number; end: number } | null>(null);
  const [active, setActive] = useState<string | null>(null);
  const [offscreen, setOffscreen] = useState<Set<string>>(new Set());

  const jumpTo = (c: Comment) => {
    if (rootRef.current) scrollReaderToAnchor(rootRef.current, content, c.anchor);
    setActive(c.id);
  };

  // Paint precise marks for each open comment; the active one gets a stronger mark.
  useEffect(() => {
    if (rootRef.current) paintHighlights(rootRef.current, content, comments, active);
  }, [comments, content, active, rootRef]);

  // When a comment becomes active (click, select, or prev/next), scroll the
  // reading pane to its anchored text. Only fires on active change — not on the
  // 3s poll refresh — so the page doesn't jump while reading.
  useEffect(() => {
    if (!active || !rootRef.current) return;
    const c = comments.find((x) => x.id === active);
    if (c) scrollReaderToAnchor(rootRef.current, content, c.anchor);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [active]);

  // Track which comments' anchored text is scrolled out of the reading pane,
  // so their cards can offer a "scroll to text" button.
  useEffect(() => {
    const root = rootRef.current;
    const pane = root?.closest(".reader-pane") as HTMLElement | null;
    if (!root || !pane) return;
    let raf = 0;
    const compute = () => {
      raf = 0;
      const pr = pane.getBoundingClientRect();
      const next = new Set<string>();
      for (const c of comments) {
        if (c.status === "resolved") continue;
        const r = rangeForAnchor(root, content, c.anchor);
        if (!r) continue;
        const b = r.getBoundingClientRect();
        if (b.bottom < pr.top + 8 || b.top > pr.bottom - 8) next.add(c.id);
      }
      setOffscreen(next);
    };
    const onScroll = () => { if (!raf) raf = requestAnimationFrame(compute); };
    compute();
    pane.addEventListener("scroll", onScroll, { passive: true });
    return () => { pane.removeEventListener("scroll", onScroll); if (raf) cancelAnimationFrame(raf); };
  }, [comments, content, rootRef]);

  // Click a marked passage → open its thread.
  useEffect(() => {
    const root = rootRef.current;
    if (!root) return;
    const onClick = (e: MouseEvent) => {
      const sel = window.getSelection();
      if (sel && !sel.isCollapsed) return; // mid-selection, not a click
      const id = commentAtPoint(root, content, comments, e.clientX, e.clientY);
      if (id) setActive(id);
    };
    root.addEventListener("click", onClick);
    return () => root.removeEventListener("click", onClick);
  }, [content, comments, rootRef]);

  // Select prose → if the selection lands on an existing comment, surface that
  // thread on the right; otherwise offer to create a new comment.
  useEffect(() => {
    const onUp = () => {
      const selection = window.getSelection();
      if (!selection || selection.isCollapsed || !rootRef.current) return setPending(null);
      const range = selection.getRangeAt(0);
      if (!rootRef.current.contains(range.commonAncestorContainer)) return setPending(null);
      const a = computeAnchor(content, rootRef.current, range);
      if (!a) return setPending(null);
      // tightest existing comment overlapping the selection
      const hit = comments
        .filter((c) => c.status !== "resolved" && c.anchor.start < a.end && c.anchor.end > a.start)
        .sort((x, y) => (x.anchor.end - x.anchor.start) - (y.anchor.end - y.anchor.start))[0];
      if (hit) { setActive(hit.id); setPending(null); }
      else { setPending(a); }
    };
    document.addEventListener("mouseup", onUp);
    return () => document.removeEventListener("mouseup", onUp);
  }, [content, comments, rootRef]);

  const idx = comments.findIndex((c) => c.id === active);
  const total = comments.length;
  const go = (delta: number) => {
    if (!total) return;
    const base = idx < 0 ? (delta > 0 ? -1 : 0) : idx;
    setActive(comments[(base + delta + total) % total].id);
  };

  return (
    <div className="margin">
      {total > 0 && (
        <div className="margin-nav">
          <button onClick={() => go(-1)} title="Previous comment" aria-label="Previous comment">‹</button>
          <span className="pos">{idx >= 0 ? idx + 1 : "–"} / {total}</span>
          <button onClick={() => go(1)} title="Next comment" aria-label="Next comment">›</button>
        </div>
      )}
      {pending && (
        <div className="margin-new">
          <button onClick={async () => { await postComment(docId, pending); setPending(null); window.getSelection()?.removeAllRanges(); onChange(); }}>
            Comment on selection
          </button>
        </div>
      )}
      {comments.map((c) => (
        <Card
          key={c.id}
          comment={c}
          currentContent={content}
          active={c.id === active}
          offscreen={offscreen.has(c.id)}
          onActivate={() => setActive(c.id)}
          onJump={() => jumpTo(c)}
          onChange={onChange}
        />
      ))}
    </div>
  );
}
