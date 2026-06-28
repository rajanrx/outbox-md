import { useEffect, useRef, useState } from "react";
import { listDocs, getDoc, type DocView } from "./api";
import { Sidebar } from "./docs/Sidebar";
import { Reader } from "./reader/Reader";
import { Margin } from "./comments/Margin";
import { useComments } from "./comments/useComments";

export default function App() {
  const [docs, setDocs] = useState<{ id: string; path: string }[]>([]);
  const [docId, setDocId] = useState("");
  const [view, setView] = useState<DocView | null>(null);
  const rootRef = useRef<HTMLDivElement>(null);
  const { comments, refresh } = useComments(docId);

  useEffect(() => { listDocs().then((d) => { setDocs(d ?? []); if (d?.length) setDocId(d[0].id); }); }, []);
  useEffect(() => { if (docId) getDoc(docId).then(setView); }, [docId, comments]);

  if (!docs.length) return <div style={{ padding: 24 }}>No documents. Mount a folder of .md files.</div>;
  return (
    <div style={{ display: "flex", height: "100vh", fontFamily: "system-ui" }}>
      <Sidebar docs={docs} activeId={docId} onSelect={setDocId} />
      <div style={{ flex: 1, overflowY: "auto" }}>
        {view && <Reader content={view.content} rootRef={rootRef} />}
      </div>
      {view && (
        <Margin docId={docId} content={view.content} rootRef={rootRef} comments={comments} onChange={refresh} />
      )}
    </div>
  );
}
