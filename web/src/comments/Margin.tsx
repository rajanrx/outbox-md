import { useEffect, useRef, useState } from "react";
import { computeAnchor } from "../anchor/anchor";
import { rangeForAnchor, commentAtPoint } from "../anchor/highlight";
import { postComment, reply, type Comment } from "../api";
import { Card } from "./Card";
import "./comments.css";

const CSSh = CSS as any;
const canHighlight = () => "Highlight" in window && CSSh.highlights;

function paintHighlights(root: HTMLElement, source: string, comments: Comment[], focusedId: string | null) {
  if (!canHighlight()) return;
  const ranges: Range[] = [];
  let focusedRange: Range | null = null;
  for (const c of comments) {
    if (c.status === "resolved") continue;
    const r = rangeForAnchor(root, source, c.anchor);
    if (!r) continue;
    if (c.id === focusedId) focusedRange = r;
    else ranges.push(r);
  }
  CSSh.highlights.set("comment", new (window as any).Highlight(...ranges));
  if (focusedRange) CSSh.highlights.set("comment-active", new (window as any).Highlight(focusedRange));
  else CSSh.highlights.delete("comment-active");
}

function paneOf(root: HTMLElement) {
  return root.closest(".reader-pane") as HTMLElement | null;
}

// Scroll the reading pane so a comment's anchored text is comfortably in view.
function scrollReaderToAnchor(root: HTMLElement, source: string, anchor: { start: number; end: number }) {
  const r = rangeForAnchor(root, source, anchor);
  const pane = paneOf(root);
  if (!r || !pane) return;
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
  const [draft, setDraft] = useState("");
  const [focused, setFocused] = useState<string | null>(null);
  const [offscreen, setOffscreen] = useState<Set<string>>(new Set());
  const [pinned, setPinned] = useState<Set<string>>(new Set());
  const lockUntil = useRef(0); // suppress scroll-driven focus during programmatic scroll

  // Load pins for the current doc (persisted in localStorage).
  useEffect(() => {
    try { setPinned(new Set(JSON.parse(localStorage.getItem(`outbox.pins.${docId}`) || "[]"))); }
    catch { setPinned(new Set()); }
  }, [docId]);
  useEffect(() => {
    localStorage.setItem(`outbox.pins.${docId}`, JSON.stringify([...pinned]));
  }, [pinned, docId]);

  const togglePin = (id: string) =>
    setPinned((prev) => { const n = new Set(prev); n.has(id) ? n.delete(id) : n.add(id); return n; });

  // Jump the reader to a comment and focus it (explicit navigation).
  const jumpTo = (c: Comment) => {
    lockUntil.current = performance.now() + 700;
    setFocused(c.id);
    if (rootRef.current) scrollReaderToAnchor(rootRef.current, content, c.anchor);
  };

  // Paint precise marks; the focused comment gets the stronger mark.
  useEffect(() => {
    if (rootRef.current) paintHighlights(rootRef.current, content, comments, focused);
  }, [comments, content, focused, rootRef]);

  // As the reader scrolls, focus the comment nearest the top of the viewport and
  // track which comments are off-screen (so pinned cards can offer a jump button).
  useEffect(() => {
    const root = rootRef.current;
    const pane = root && paneOf(root);
    if (!root || !pane) return;
    let raf = 0;
    const compute = () => {
      raf = 0;
      const pr = pane.getBoundingClientRect();
      const anchorLine = pr.top + 120;
      const off = new Set<string>();
      let best: string | null = null;
      let bestDist = Infinity;
      for (const c of comments) {
        if (c.status === "resolved") continue;
        const r = rangeForAnchor(root, content, c.anchor);
        if (!r) continue;
        const b = r.getBoundingClientRect();
        if (b.bottom < pr.top + 8 || b.top > pr.bottom - 8) off.add(c.id);
        const dist = Math.abs(b.top - anchorLine);
        if (dist < bestDist) { bestDist = dist; best = c.id; }
      }
      setOffscreen(off);
      if (best && performance.now() >= lockUntil.current) setFocused(best);
    };
    const onScroll = () => { if (!raf) raf = requestAnimationFrame(compute); };
    compute();
    pane.addEventListener("scroll", onScroll, { passive: true });
    return () => { pane.removeEventListener("scroll", onScroll); if (raf) cancelAnimationFrame(raf); };
  }, [comments, content, rootRef]);

  // Click a marked passage → focus its thread.
  useEffect(() => {
    const root = rootRef.current;
    if (!root) return;
    const onClick = (e: MouseEvent) => {
      const sel = window.getSelection();
      if (sel && !sel.isCollapsed) return;
      const id = commentAtPoint(root, content, comments, e.clientX, e.clientY);
      if (id) setFocused(id);
    };
    root.addEventListener("click", onClick);
    return () => root.removeEventListener("click", onClick);
  }, [content, comments, rootRef]);

  // Select prose → surface an existing comment, or offer to create one.
  useEffect(() => {
    const onUp = () => {
      const selection = window.getSelection();
      if (!selection || selection.isCollapsed || !rootRef.current) return setPending(null);
      const range = selection.getRangeAt(0);
      if (!rootRef.current.contains(range.commonAncestorContainer)) return setPending(null);
      const a = computeAnchor(content, rootRef.current, range);
      if (!a) return setPending(null);
      const hit = comments
        .filter((c) => c.status !== "resolved" && c.anchor.start < a.end && c.anchor.end > a.start)
        .sort((x, y) => (x.anchor.end - x.anchor.start) - (y.anchor.end - y.anchor.start))[0];
      if (hit) { setFocused(hit.id); setPending(null); }
      else { setPending(a); }
    };
    document.addEventListener("mouseup", onUp);
    return () => document.removeEventListener("mouseup", onUp);
  }, [content, comments, rootRef]);

  const submitComment = async () => {
    if (!pending) return;
    const c = await postComment(docId, pending);
    if (draft.trim()) await reply(c.id, draft.trim());
    setPending(null); setDraft("");
    window.getSelection()?.removeAllRanges();
    onChange();
    setFocused(c.id);
  };

  // One comment at a time (the nearby/focused one) plus any pinned comments.
  const focusedC = comments.find((c) => c.id === focused) || null;
  const shown: Comment[] = [];
  if (focusedC) shown.push(focusedC);
  for (const c of comments) if (pinned.has(c.id) && c.id !== focused) shown.push(c);

  const idx = comments.findIndex((c) => c.id === focused);
  const total = comments.length;
  const go = (delta: number) => {
    if (!total) return;
    const base = idx < 0 ? (delta > 0 ? -1 : 0) : idx;
    jumpTo(comments[(base + delta + total) % total]);
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
          <textarea
            autoFocus value={draft} placeholder="Add a comment…"
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => { if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) submitComment(); }}
          />
          <div className="margin-new-actions">
            <button className="ghost" onClick={() => { setPending(null); setDraft(""); window.getSelection()?.removeAllRanges(); }}>Cancel</button>
            <button className="primary" onClick={submitComment}>Comment</button>
          </div>
        </div>
      )}

      {shown.map((c) => (
        <Card
          key={c.id}
          comment={c}
          currentContent={content}
          active={c.id === focused}
          pinned={pinned.has(c.id)}
          offscreen={offscreen.has(c.id)}
          onActivate={() => setFocused(c.id)}
          onJump={() => jumpTo(c)}
          onTogglePin={() => togglePin(c.id)}
          onChange={onChange}
        />
      ))}

      {!shown.length && !pending && (
        <div className="margin-empty">
          {total > 0 ? "Scroll to a highlighted passage, or use ‹ › to step through comments." : "Select text in the document to add a comment."}
        </div>
      )}
    </div>
  );
}
