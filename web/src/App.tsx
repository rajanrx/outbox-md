import { useEffect, useState } from "react";
import { listDocs, getDoc, type DocView, type Comment } from "./api";
import { Editor } from "./components/Editor";
import { Outbox } from "./components/Outbox";
import { SuggestionView } from "./components/Suggestion";

export default function App() {
  const [docId, setDocId] = useState("");
  const [view, setView] = useState<DocView | null>(null);
  const [sel, setSel] = useState<Comment | null>(null);

  const refresh = async (id: string) => setView(await getDoc(id));

  useEffect(() => {
    listDocs().then((ds) => {
      if (ds?.length) setDocId(ds[0].id);
    });
  }, []);
  useEffect(() => {
    if (!docId) return;
    refresh(docId);
    const t = setInterval(() => refresh(docId), 2000);
    return () => clearInterval(t);
  }, [docId]);

  if (!view) return <div style={{ padding: 24 }}>No documents. Mount a folder of .md files.</div>;
  return (
    <div style={{ display: "flex", gap: 24, padding: 24, fontFamily: "system-ui" }}>
      <div style={{ flex: 2 }}>
        <h2>{view.document.path}</h2>
        <Editor docId={docId} content={view.content} onChange={() => refresh(docId)} />
      </div>
      <div style={{ flex: 1 }}>
        <Outbox comments={view.comments} onSelect={setSel} />
        {sel && <SuggestionView commentId={sel.id} onAccepted={() => { setSel(null); refresh(docId); }} />}
      </div>
    </div>
  );
}
