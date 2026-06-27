import { useEffect, useState } from "react";
import { getSuggestion, accept, type Suggestion } from "../api";

export function SuggestionView({ commentId, onAccepted }: {
  commentId: string;
  onAccepted: () => void;
}) {
  const [sg, setSg] = useState<Suggestion | null>(null);
  useEffect(() => {
    getSuggestion(commentId).then(setSg);
  }, [commentId]);
  if (!sg) return <p>No suggestion yet for this comment.</p>;
  return (
    <div>
      <h4>Proposed content</h4>
      <pre style={{ whiteSpace: "pre-wrap", background: "#f4f4f4", padding: 8 }}>
        {sg.proposedContent}
      </pre>
      <button onClick={async () => { await accept(commentId); onAccepted(); }}>Accept</button>
    </div>
  );
}
