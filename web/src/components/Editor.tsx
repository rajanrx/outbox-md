import { useState } from "react";
import { selectionToAnchor, type Anchor } from "../lib/selection";
import { postComment } from "../api";

export function Editor({ docId, content, onChange }: {
  docId: string;
  content: string;
  onChange: () => void;
}) {
  const [sel, setSel] = useState<Anchor | null>(null);
  return (
    <div>
      <textarea
        readOnly
        value={content}
        style={{ width: "100%", height: 400, fontFamily: "monospace" }}
        onSelect={(e) => {
          const t = e.currentTarget;
          setSel(selectionToAnchor(content, t.selectionStart, t.selectionEnd));
        }}
      />
      <button
        disabled={!sel || sel.start === sel.end}
        onClick={async () => {
          if (sel) {
            await postComment(docId, sel);
            setSel(null);
            onChange();
          }
        }}
      >
        Comment on selection
      </button>
    </div>
  );
}
