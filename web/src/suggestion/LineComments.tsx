import { lineKey, type LineDraft, type LineRef } from "./refine";

// LineCommentApi is the controlled contract the DiffModal passes down so a diff
// row can render its inline-comment affordance, drafts and editor without owning
// any state itself. When absent (folder-changes view, historical diffs, the
// compact card excerpt) the diff renders exactly as before.
export type LineCommentApi = {
  // Drafts grouped by lineKey(side, lineNo) for O(1) per-row lookup.
  drafts: Map<string, LineDraft[]>;
  // The line whose editor is currently open. id set → editing an existing draft;
  // id absent → composing a new draft. value is the live textarea contents.
  editing: { key: string; id?: string; value: string } | null;
  onAddClick: (ref: LineRef) => void; // open a fresh editor for this line
  onEditClick: (draft: LineDraft) => void; // open the editor on an existing draft
  onEditingChange: (value: string) => void; // textarea keystroke
  onSave: (ref: LineRef) => void; // commit the open editor
  onCancel: () => void; // discard the open editor
  onRemove: (id: string) => void; // delete a saved draft
};

// hasLineContent reports whether a line currently has anything to render (a saved
// draft or an open composer). Split view uses it to avoid emitting an empty
// full-width grid row under lines with no comments.
export function hasLineContent(api: LineCommentApi, lineRef: LineRef): boolean {
  const key = lineKey(lineRef.side, lineRef.lineNo);
  if ((api.drafts.get(key)?.length ?? 0) > 0) return true;
  return api.editing?.key === key;
}

// AddCommentButton is the hover "＋" affordance. It sits in a diff line's gutter
// (split) or at the row's leading edge (inline) and opens the editor for `ref`.
export function AddCommentButton({ api, lineRef }: { api: LineCommentApi; lineRef: LineRef }) {
  return (
    <button
      type="button"
      className="line-comment-add"
      aria-label={`Comment on line ${lineRef.lineNo}`}
      title="Add inline comment"
      onClick={(e) => {
        e.stopPropagation();
        api.onAddClick(lineRef);
      }}
    >
      +
    </button>
  );
}

// LineCommentZone renders, for one diff line, its saved drafts and (when open) the
// inline editor. It returns null when there is nothing to show, so it can be
// dropped under every commentable row cheaply. It spans the full diff width via
// its wrapper's grid-column in split; in inline it is a normal block.
export function LineCommentZone({ api, lineRef }: { api: LineCommentApi; lineRef: LineRef }) {
  const key = lineKey(lineRef.side, lineRef.lineNo);
  const drafts = api.drafts.get(key) ?? [];
  const composing = api.editing?.key === key && api.editing?.id === undefined;
  if (drafts.length === 0 && !composing) return null;

  return (
    <div className="line-comment-zone">
      {drafts.map((d) => {
        const isEditing = api.editing?.key === key && api.editing?.id === d.id;
        return isEditing ? (
          <Editor key={d.id} api={api} lineRef={lineRef} saveLabel="Update" />
        ) : (
          <div key={d.id} className="line-comment">
            <span className="line-comment-body">{d.body}</span>
            <span className="line-comment-tools">
              <button type="button" className="line-comment-link" onClick={() => api.onEditClick(d)}>
                Edit
              </button>
              <button type="button" className="line-comment-link" onClick={() => api.onRemove(d.id)}>
                Remove
              </button>
            </span>
          </div>
        );
      })}
      {composing && <Editor api={api} lineRef={lineRef} saveLabel="Save" />}
    </div>
  );
}

function Editor({ api, lineRef, saveLabel }: { api: LineCommentApi; lineRef: LineRef; saveLabel: string }) {
  const value = api.editing?.value ?? "";
  return (
    <div className="line-comment-editor">
      <textarea
        autoFocus
        value={value}
        placeholder="Leave feedback on this line…"
        onChange={(e) => api.onEditingChange(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
            e.preventDefault();
            api.onSave(lineRef);
          } else if (e.key === "Escape") {
            e.preventDefault();
            api.onCancel();
          }
        }}
      />
      <div className="line-comment-editor-actions">
        <button type="button" className="line-comment-link" onClick={api.onCancel}>
          Cancel
        </button>
        <button
          type="button"
          className="line-comment-link primary"
          disabled={!value.trim()}
          onClick={() => api.onSave(lineRef)}
        >
          {saveLabel}
        </button>
      </div>
    </div>
  );
}
