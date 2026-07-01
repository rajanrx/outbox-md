import { useEffect, useRef, useState } from "react";
import { computeAnchor } from "../anchor/anchor";
import { rangeForAnchor, commentAtPoint } from "../anchor/highlight";
import { postComment, reply, type Comment } from "../api";
import { Card } from "./Card";
import "./comments.css";

const CSSh = CSS as any;
const canHighlight = () => "Highlight" in window && CSSh.highlights;

function paintHighlights(
  root: HTMLElement,
  source: string,
  comments: Comment[],
  focusedId: string | null,
  pending: { start: number; end: number } | null,
) {
  if (!canHighlight()) return;
  const ranges: Range[] = [];
  let activeRange: Range | null = null;
  for (const c of comments) {
    if (c.status === "resolved") continue;
    const r = rangeForAnchor(root, source, c.anchor);
    if (!r) continue;
    if (c.id === focusedId) activeRange = r;
    else ranges.push(r);
  }
  // A pending (locked-in) comment keeps its passage marked while the composer is
  // open, so the highlight never "disappears" the moment the native selection is
  // collapsed by clicking the composer.
  if (pending) {
    const pr = rangeForAnchor(root, source, pending);
    if (pr) activeRange = pr;
  }
  CSSh.highlights.set("comment", new (window as any).Highlight(...ranges));
  if (activeRange) CSSh.highlights.set("comment-active", new (window as any).Highlight(activeRange));
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

export function Margin({ docId, content, docPath, hasGit = false, rootRef, comments, reloadKey = 0, onChange }: {
  docId: string;
  content: string;
  docPath: string;
  hasGit?: boolean;
  rootRef: React.RefObject<HTMLDivElement | null>;
  comments: Comment[];
  // Bumped on each SSE event for the open doc; forwarded to the open Card so its
  // thread re-fetches in place (not just the comment list).
  reloadKey?: number;
  onChange: () => void;
}) {
  const [pending, setPending] = useState<{ start: number; end: number } | null>(null);
  const [draft, setDraft] = useState("");
  const [focused, setFocused] = useState<string | null>(null);
  const [offscreen, setOffscreen] = useState<Set<string>>(new Set());
  const [pinned, setPinned] = useState<Set<string>>(new Set());
  // anchored DOM ranges, computed off the scroll path so scrolling stays smooth
  const rangeCache = useRef<Map<string, Range>>(new Map());
  // the comments panel itself — mouseups inside it must not disturb a pending
  // comment the user is composing.
  const marginRef = useRef<HTMLDivElement>(null);

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
    setFocused(c.id);
    if (rootRef.current) scrollReaderToAnchor(rootRef.current, content, c.anchor);
  };

  // Paint precise marks; the focused (or pending) comment gets the stronger mark.
  useEffect(() => {
    if (rootRef.current) paintHighlights(rootRef.current, content, comments, focused, pending);
  }, [comments, content, focused, pending, rootRef]);

  // Rebuild the anchored-range cache only when comments/content change (the
  // expensive diff-match-patch mapping). Never on scroll.
  useEffect(() => {
    const root = rootRef.current;
    if (!root) return;
    const m = new Map<string, Range>();
    for (const c of comments) {
      if (c.status === "resolved") continue;
      const r = rangeForAnchor(root, content, c.anchor);
      if (r) m.set(c.id, r);
    }
    rangeCache.current = m;
  }, [comments, content, rootRef]);

  // Track which comments' anchored text is off-screen so a focused/pinned card
  // can offer a "scroll to text" button. The shown comment never changes on
  // scroll — only on click or prev/next. This runs on the scroll path, so it
  // only reads cached rects (cheap) and only sets state when the set changes.
  useEffect(() => {
    const root = rootRef.current;
    const pane = root && paneOf(root);
    if (!pane) return;
    let raf = 0;
    const compute = () => {
      raf = 0;
      const pr = pane.getBoundingClientRect();
      const off = new Set<string>();
      for (const [cid, r] of rangeCache.current) {
        const b = r.getBoundingClientRect();
        if (b.bottom < pr.top + 8 || b.top > pr.bottom - 8) off.add(cid);
      }
      setOffscreen((prev) => (prev.size === off.size && [...off].every((x) => prev.has(x)) ? prev : off));
    };
    const onScroll = () => { if (!raf) raf = requestAnimationFrame(compute); };
    compute();
    pane.addEventListener("scroll", onScroll, { passive: true });
    return () => { pane.removeEventListener("scroll", onScroll); if (raf) cancelAnimationFrame(raf); };
  }, [comments, rootRef]);

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

  // Pointer cursor over commented passages. The CSS Custom Highlight API can't
  // set `cursor`, so hit-test the pointer against the cached anchor ranges (the
  // exact highlighted text) on a rAF-throttled mousemove and toggle the cursor —
  // no diff-match-patch work on the hover path.
  useEffect(() => {
    const root = rootRef.current;
    if (!root) return;
    let raf = 0;
    let lx = 0, ly = 0;
    const overComment = (x: number, y: number) => {
      for (const r of rangeCache.current.values()) {
        const rects = r.getClientRects();
        for (let i = 0; i < rects.length; i++) {
          const b = rects[i];
          if (x >= b.left && x <= b.right && y >= b.top && y <= b.bottom) return true;
        }
      }
      return false;
    };
    const apply = () => { raf = 0; root.style.cursor = overComment(lx, ly) ? "pointer" : ""; };
    const onMove = (e: MouseEvent) => { lx = e.clientX; ly = e.clientY; if (!raf) raf = requestAnimationFrame(apply); };
    root.addEventListener("mousemove", onMove);
    return () => {
      root.removeEventListener("mousemove", onMove);
      if (raf) cancelAnimationFrame(raf);
      root.style.cursor = "";
    };
  }, [comments, rootRef]);

  // Select prose → surface an existing comment, or offer to create one. Once a
  // pending comment is opened it stays "locked in" on the right until the user
  // submits or cancels it: a collapsed selection (e.g. clicking the composer,
  // which clears the native highlight) must NOT discard it, and mouseups inside
  // the comments panel are ignored entirely so clicking Comment/Cancel/the
  // textarea can't tear the composer down before submitComment runs.
  useEffect(() => {
    const onUp = (e: MouseEvent) => {
      if (e.target instanceof Node && marginRef.current?.contains(e.target)) return;
      const selection = window.getSelection();
      if (!selection || selection.isCollapsed || !rootRef.current) return;
      const range = selection.getRangeAt(0);
      if (!rootRef.current.contains(range.commonAncestorContainer)) return;
      const a = computeAnchor(content, rootRef.current, range);
      if (!a) return;
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

  // Navigate only open comments — resolved ones have no highlight, so landing on
  // them scrolls to an unmarked spot. Resolved are reachable only by their thread.
  const navComments = comments.filter((c) => c.status !== "resolved");
  const idx = navComments.findIndex((c) => c.id === focused);
  const total = navComments.length;
  const go = (delta: number) => {
    if (!total) return;
    const base = idx < 0 ? (delta > 0 ? -1 : 0) : idx;
    jumpTo(navComments[(base + delta + total) % total]);
  };

  return (
    <div className="margin" ref={marginRef}>
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
          docPath={docPath}
          hasGit={hasGit}
          active={c.id === focused}
          pinned={pinned.has(c.id)}
          offscreen={offscreen.has(c.id)}
          reloadKey={reloadKey}
          onActivate={() => setFocused(c.id)}
          onJump={() => jumpTo(c)}
          onTogglePin={() => togglePin(c.id)}
          onChange={onChange}
        />
      ))}

      {!shown.length && !pending && (
        <div className="margin-empty">
          {total > 0 ? "Click a highlighted passage, or use ‹ › to step through comments." : "Select text in the document to add a comment."}
        </div>
      )}
    </div>
  );
}
