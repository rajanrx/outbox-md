import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { getPendingSuggestions, type PendingSuggestion } from "../api";
import { DiffPanel } from "./DiffPanel";
import { DiffRows, counts } from "./DiffRows";
import { unifiedDiff } from "./diff";
import "./diff.css";

// DiffModal is the full-screen review surface for a single suggestion. It shows
// "This change" (the suggestion's own single-file diff, with Approve/Reject via
// the reused DiffPanel) and a "Folder changes" view of every OTHER doc across
// the project that currently has a pending suggestion — built from outbox-md's
// own data (no git), so it is always available. The folder view is context only:
// Approve applies just this one suggestion.
export function DiffModal({ open, commentId, currentContent, title, onClose, onChange }: {
  open: boolean;
  commentId: string;
  currentContent: string;
  title: string;
  onClose: () => void;
  onChange: () => void;
}) {
  const [pending, setPending] = useState<PendingSuggestion[] | null>(null);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") onClose(); };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  // Fetch pending suggestions lazily, only when the modal opens.
  useEffect(() => {
    if (!open) { setPending(null); return; }
    let live = true;
    getPendingSuggestions().then((p) => { if (live) setPending(p); });
    return () => { live = false; };
  }, [open]);

  if (!open) return null;

  // Approving/rejecting closes the modal and bubbles the change up so the thread
  // and folder view refresh from the parent.
  const done = () => { onChange(); onClose(); };

  // Exclude this comment's own suggestion — it is already shown in "This change"
  // above. Sibling comments on the same doc stay visible.
  const others = (pending ?? []).filter((p) => p.commentId !== commentId);

  // Portal to <body> so the fixed-position backdrop is measured against the
  // viewport, not a clipping ancestor (Card sits deep in the tree). This mirrors
  // why the app's other Modal renders from the top level.
  return createPortal(
    <div className="modal-backdrop" role="presentation" onMouseDown={onClose}>
      <div
        className="modal-card diff-modal"
        role="dialog"
        aria-modal="true"
        aria-label={`Review change to ${title}`}
        onMouseDown={(e) => e.stopPropagation()}
      >
        <div className="diff-modal-head">
          <h2 className="modal-title">{title}</h2>
          <button className="ic-btn" aria-label="Close" title="Close" onClick={onClose}>✕</button>
        </div>

        <div className="diff-modal-body">
          <section className="diff-section">
            <div className="diff-section-title">This change</div>
            {/* DiffPanel fetches the suggestion and carries the Approve/Reject
                actions; onDone closes the modal and refreshes the parent. */}
            <DiffPanel commentId={commentId} currentContent={currentContent} onDone={done} />
          </section>

          <section className="diff-section">
            <div className="diff-section-title">Folder changes</div>
            {pending === null ? (
              <div className="diff-empty">Loading folder changes…</div>
            ) : others.length === 0 ? (
              <div className="diff-empty">No other pending suggestions across the project.</div>
            ) : (
              others.map((p, idx) => {
                const rows = unifiedDiff(p.current, p.proposed);
                const c = counts(rows);
                return (
                  <details key={p.commentId} className="folder-file" open={idx === 0}>
                    <summary className="folder-file-head">
                      <span className="folder-file-path">{p.path}</span>
                      <span className="folder-file-counts">
                        <span className="ins">+{c.ins}</span> <span className="del">−{c.del}</span>
                      </span>
                    </summary>
                    <DiffRows rows={rows} />
                  </details>
                );
              })
            )}
          </section>
        </div>
      </div>
    </div>,
    document.body,
  );
}
