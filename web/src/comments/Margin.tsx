import { useEffect, useState } from "react";
import { computeAnchor } from "../anchor/anchor";
import { postComment, type Comment } from "../api";
import { Card } from "./Card";
import "./comments.css";

function paintHighlights(root: HTMLElement, comments: Comment[]) {
  const CSSh = CSS as any;
  if (!("Highlight" in window) || !CSSh.highlights) return;
  const ranges: Range[] = [];
  for (const c of comments) {
    const block = [...root.querySelectorAll<HTMLElement>("[data-pos-start]")].find((el) => {
      const s = Number(el.getAttribute("data-pos-start"));
      const e = Number(el.getAttribute("data-pos-end"));
      return c.anchor.start >= s && c.anchor.end <= e;
    });
    if (!block) continue;
    const r = document.createRange();
    try { r.selectNodeContents(block); ranges.push(r); } catch { /* ignore */ }
  }
  CSSh.highlights.set("comment", new (window as any).Highlight(...ranges));
}

export function Margin({ docId, content, rootRef, comments, onChange }: {
  docId: string;
  content: string;
  rootRef: React.RefObject<HTMLDivElement | null>;
  comments: Comment[];
  onChange: () => void;
}) {
  const [pending, setPending] = useState<{ start: number; end: number } | null>(null);

  useEffect(() => {
    if (rootRef.current) paintHighlights(rootRef.current, comments);
  }, [comments, rootRef]);

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
      {comments.map((c) => <Card key={c.id} comment={c} currentContent={content} onChange={onChange} />)}
    </div>
  );
}
