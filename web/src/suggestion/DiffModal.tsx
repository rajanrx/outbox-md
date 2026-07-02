import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import {
  accept,
  getPendingSuggestions,
  getSuggestion,
  rejectSuggestion,
  type PendingSuggestion,
  type Suggestion,
} from "../api";
import { counts } from "./DiffRows";
import { unifiedDiff } from "./diff";
import { DiffToggle, DiffView, useDiffView } from "./view";
import "./diff.css";

// DiffModal is the near-full-screen review surface for a single suggestion. The
// header (file path + Side-by-side/Inline toggle + close) and footer
// (Approve/Reject) stay pinned while the diff area scrolls. It shows:
//  • "This change" — the suggestion's own diff, in the chosen view.
//  • "Folder changes" — every OTHER doc across the project that currently has a
//    pending suggestion (built from outbox-md's own data, no git), shown in the
//    same view. Context only: Approve applies just this one suggestion.
export function DiffModal({ open, commentId, currentContent, title, onClose, onChange }: {
  open: boolean;
  commentId: string;
  currentContent: string;
  title: string;
  onClose: () => void;
  onChange: () => void;
}) {
  const [sg, setSg] = useState<Suggestion | null>(null);
  const [pending, setPending] = useState<PendingSuggestion[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [mode, setMode] = useDiffView();

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") onClose(); };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  // Fetch the target suggestion + folder-wide pending suggestions lazily, only
  // when the modal opens.
  useEffect(() => {
    if (!open) { setSg(null); setPending(null); return; }
    let live = true;
    getSuggestion(commentId).then((s) => { if (live) setSg(s); });
    getPendingSuggestions().then((p) => { if (live) setPending(p); });
    return () => { live = false; };
  }, [open, commentId]);

  if (!open) return null;

  // Approving/rejecting closes the modal and bubbles the change up so the thread
  // and folder view refresh from the parent.
  const act = async (fn: () => Promise<unknown>) => {
    setBusy(true);
    try { await fn(); onChange(); onClose(); } finally { setBusy(false); }
  };

  // Exclude this comment's own suggestion — already shown in "This change".
  const others = (pending ?? []).filter((p) => p.commentId !== commentId);
  const proposable = !!sg && sg.state === "proposed";
  // A proposed suggestion diffs against the CURRENT content (what accepting it
  // would change). An accepted/rejected one is a read-only HISTORICAL diff:
  // against-version → proposed, since post-accept the current content already
  // equals the proposed content (a current-vs-proposed diff would show nothing).
  const before = proposable ? currentContent : (sg?.againstContent ?? currentContent);
  const changed = !!sg && sg.proposedContent !== before;
  const stateLabel = sg?.state === "accepted" ? "Accepted" : sg?.state === "rejected" ? "Rejected" : sg?.state;

  // Portal to <body> so the fixed-position backdrop is measured against the
  // viewport, not a clipping ancestor.
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
          <h2 className="modal-title" title={title}>{title}</h2>
          <div className="diff-modal-head-tools">
            <DiffToggle mode={mode} onChange={setMode} />
            <button className="ic-btn" aria-label="Close" title="Close" onClick={onClose}>✕</button>
          </div>
        </div>

        <div className="diff-modal-body">
          <section className="diff-section">
            <div className="diff-section-title">This change</div>
            {sg === null ? (
              <div className="diff-empty">Loading change…</div>
            ) : changed ? (
              <div className="diff-frame">
                <DiffView before={before} after={sg.proposedContent} mode={mode} />
              </div>
            ) : (
              <div className="diff-empty">No textual changes from the current version.</div>
            )}
          </section>

          <section className="diff-section">
            <div className="diff-section-title">Folder changes</div>
            {pending === null ? (
              <div className="diff-empty">Loading folder changes…</div>
            ) : others.length === 0 ? (
              <div className="diff-empty">No other pending suggestions across the project.</div>
            ) : (
              others.map((p, idx) => {
                const c = counts(unifiedDiff(p.current, p.proposed));
                return (
                  <details key={p.commentId} className="folder-file" open={idx === 0}>
                    <summary className="folder-file-head">
                      <span className="folder-file-path">{p.path}</span>
                      <span className="folder-file-counts">
                        <span className="ins">+{c.ins}</span> <span className="del">−{c.del}</span>
                      </span>
                    </summary>
                    <div className="diff-frame">
                      <DiffView before={p.current} after={p.proposed} mode={mode} />
                    </div>
                  </details>
                );
              })
            )}
          </section>
        </div>

        <div className="diff-modal-foot">
          {sg && !proposable ? (
            <span className={`suggestion-state state-${sg.state}`}>{stateLabel}</span>
          ) : (
            <span className="diff-foot-spacer" />
          )}
          {/* Accept/Reject only for a live proposed suggestion; an accepted/
              rejected one is read-only (the status label above is shown instead). */}
          {proposable && (
            <div className="diff-foot-actions">
              <button disabled={busy} onClick={() => act(() => rejectSuggestion(commentId))}>
                Reject
              </button>
              <button className="primary" disabled={busy} onClick={() => act(() => accept(commentId))}>
                Approve
              </button>
            </div>
          )}
        </div>
      </div>
    </div>,
    document.body,
  );
}
