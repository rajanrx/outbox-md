import { useEffect } from "react";

type ModalProps = {
  open: boolean;
  title: string;
  body: string;
  confirmLabel: string;
  onConfirm: () => void;
  onCancel: () => void;
};

// Modal is a small reusable confirmation dialog. Escape and a backdrop click
// both cancel; only the confirm button confirms. It renders nothing when closed.
export function Modal({ open, title, body, confirmLabel, onConfirm, onCancel }: ModalProps) {
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onCancel();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open, onCancel]);

  if (!open) return null;
  return (
    // Backdrop click cancels; the card stops propagation so an in-card click
    // doesn't bubble up and close the dialog.
    <div className="modal-backdrop" role="presentation" onMouseDown={onCancel}>
      <div
        className="modal-card"
        role="dialog"
        aria-modal="true"
        aria-label={title}
        onMouseDown={(e) => e.stopPropagation()}
      >
        <h2 className="modal-title">{title}</h2>
        <p className="modal-body">{body}</p>
        <div className="modal-actions">
          <button className="gov-btn ghost" onClick={onCancel}>Cancel</button>
          <button className="gov-btn" onClick={onConfirm} autoFocus>{confirmLabel}</button>
        </div>
      </div>
    </div>
  );
}
