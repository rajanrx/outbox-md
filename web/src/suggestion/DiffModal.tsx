import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { getFolderDiff, type FolderDiff } from "../api";
import { DiffPanel } from "./DiffPanel";
import { DiffRows, counts } from "./DiffRows";
import "./diff.css";

// DiffModal is the full-screen review surface for a single suggestion. It shows
// "This change" (the suggestion's own single-file diff, with Approve/Reject via
// the reused DiffPanel) and, when the served folder is a git repo, a
// GitHub-PR-style "Folder changes" view of every changed .md file. The folder
// view is context only — Approve applies just this one suggestion.
export function DiffModal({ open, commentId, currentContent, title, hasGit, onClose, onChange }: {
  open: boolean;
  commentId: string;
  currentContent: string;
  title: string;
  hasGit: boolean;
  onClose: () => void;
  onChange: () => void;
}) {
  const [folder, setFolder] = useState<FolderDiff | null>(null);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") onClose(); };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  // Fetch the folder diff lazily, only when the modal opens on a git repo.
  useEffect(() => {
    if (!open || !hasGit) { setFolder(null); return; }
    let live = true;
    getFolderDiff().then((f) => { if (live) setFolder(f); });
    return () => { live = false; };
  }, [open, hasGit]);

  if (!open) return null;

  // Approving/rejecting closes the modal and bubbles the change up so the thread
  // and folder view refresh from the parent.
  const done = () => { onChange(); onClose(); };

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

          {hasGit && (
            <section className="diff-section">
              <div className="diff-section-title">Folder changes</div>
              {folder === null ? (
                <div className="diff-empty">Loading folder changes…</div>
              ) : folder.files.length === 0 ? (
                <div className="diff-empty">No other changed Markdown files in the folder.</div>
              ) : (
                folder.files.map((f, idx) => {
                  const c = counts(f.rows);
                  return (
                    <details key={f.path} className="folder-file" open={idx === 0}>
                      <summary className="folder-file-head">
                        <span className="folder-file-path">{f.path}</span>
                        <span className="folder-file-counts">
                          <span className="ins">+{c.ins}</span> <span className="del">−{c.del}</span>
                        </span>
                      </summary>
                      <DiffRows rows={f.rows} />
                    </details>
                  );
                })
              )}
            </section>
          )}
        </div>
      </div>
    </div>,
    document.body,
  );
}
