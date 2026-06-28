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

export function Margin({ docId, content, rootRef, comments, onChange }: {
  docId: string;
  content: string;
  rootRef: React.RefObject<HTMLDivElement | null>;
  comments: Comment[];
  onChange: () => void;
}) {
  const [pending, setPending] = useState<{ start: number; end: number } | null>(null);
  const [active, setActive] = useState<string | null>(null);

  // Paint precise marks for each open comment; the active one gets a stronger mark.
  useEffect(() => {
    if (rootRef.current) paintHighlights(rootRef.current, content, comments, active);
  }, [comments, content, active, rootRef]);

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

  // Select prose → offer to comment.
  useEffect(() => {
    const onUp = () => {
      const selection = window.getSelection();
      if (!selection || selection.isCollapsed || !rootRef.current) return setPending(null);
      const range = selection.getRangeAt(0);
      if (!rootRef.current.contains(range.commonAncestorContainer)) return setPending(null);
      setPending(computeAnchor(content, rootRef.current, range));
    };
    document.addEventListener("mouseup", onUp);
    return () => document.removeEventListener("mouseup", onUp);
  }, [content, rootRef]);

  return (
    <div className="margin">
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
          onActivate={() => setActive(c.id)}
          onChange={onChange}
        />
      ))}
    </div>
  );
}
