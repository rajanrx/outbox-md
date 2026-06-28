import { useCallback, useEffect, useRef, useState } from "react";
import { listDocs, getDoc, type DocView } from "./api";
import { FileTree } from "./docs/FileTree";
import { Reader } from "./reader/Reader";
import { Margin } from "./comments/Margin";

const PanelLeftIcon = () => (
  <svg viewBox="0 0 18 18" fill="none" stroke="currentColor" strokeWidth="1.5">
    <rect x="2.5" y="3" width="13" height="12" rx="2" />
    <path d="M7 3v12" />
    <rect x="3.6" y="5.2" width="2.4" height="1.6" rx=".4" fill="currentColor" stroke="none" />
  </svg>
);
const PanelRightIcon = () => (
  <svg viewBox="0 0 18 18" fill="none" stroke="currentColor" strokeWidth="1.5">
    <rect x="2.5" y="3" width="13" height="12" rx="2" />
    <path d="M11 3v12" />
  </svg>
);

const COMMENTS_W_KEY = "outbox.commentsWidth";
const clampW = (w: number) => Math.min(760, Math.max(300, w));

export default function App() {
  const [docs, setDocs] = useState<{ id: string; path: string }[]>([]);
  const [docId, setDocId] = useState("");
  const [view, setView] = useState<DocView | null>(null);
  const [treeOpen, setTreeOpen] = useState(true);
  const [commentsOpen, setCommentsOpen] = useState(true);
  const [commentsW, setCommentsW] = useState(() => {
    const v = Number(localStorage.getItem(COMMENTS_W_KEY));
    return v ? clampW(v) : 420;
  });
  const rootRef = useRef<HTMLDivElement>(null);

  useEffect(() => { localStorage.setItem(COMMENTS_W_KEY, String(commentsW)); }, [commentsW]);

  const startResize = (e: React.MouseEvent) => {
    e.preventDefault();
    const onMove = (ev: MouseEvent) => setCommentsW(clampW(window.innerWidth - ev.clientX));
    const onUp = () => {
      document.removeEventListener("mousemove", onMove);
      document.removeEventListener("mouseup", onUp);
      document.body.style.userSelect = "";
      document.body.style.cursor = "";
    };
    document.body.style.userSelect = "none";
    document.body.style.cursor = "col-resize";
    document.addEventListener("mousemove", onMove);
    document.addEventListener("mouseup", onUp);
  };

  const refresh = useCallback(async () => {
    if (docId) setView(await getDoc(docId));
  }, [docId]);

  useEffect(() => { listDocs().then((d) => { setDocs(d ?? []); if (d?.length) setDocId(d[0].id); }); }, []);
  useEffect(() => {
    if (!docId) return;
    refresh();
    const t = setInterval(refresh, 3000);
    return () => clearInterval(t);
  }, [docId, refresh]);

  const activePath = docs.find((d) => d.id === docId)?.path ?? "";
  const crumbs = activePath ? activePath.split("/") : [];
  const openCount = view?.comments?.filter((c) => c.status !== "resolved").length ?? 0;

  return (
    <div className="app-shell">
      <div className="topbar">
        <div className="brand"><span className="dot" /><b>outbox</b><span>·md</span></div>
        {crumbs.length > 0 && (
          <div className="crumbs">
            {crumbs.slice(0, -1).map((c, i) => (
              <span key={i} style={{ display: "contents" }}>{c}<span className="sep">/</span></span>
            ))}
            <span className="leaf">{crumbs[crumbs.length - 1]}</span>
          </div>
        )}
        <div className="spacer" />
        <button className={"icon-btn" + (treeOpen ? " on" : "")} title="Toggle files" onClick={() => setTreeOpen((v) => !v)}><PanelLeftIcon /></button>
        <button className={"icon-btn" + (commentsOpen ? " on" : "")} title="Toggle comments" onClick={() => setCommentsOpen((v) => !v)}><PanelRightIcon /></button>
      </div>

      <div className="workbench">
        <aside className={"tree-panel" + (treeOpen ? "" : " collapsed")}>
          <div className="panel-head">Explorer</div>
          <div className="panel-body"><FileTree docs={docs} activeId={docId} onSelect={setDocId} /></div>
        </aside>

        <main className="reader-pane">
          {view
            ? <Reader content={view.content} rootRef={rootRef} />
            : <div className="reader-empty">No documents — mount a folder of <code>.md</code> files.</div>}
        </main>

        <aside className={"comments-panel" + (commentsOpen ? "" : " collapsed")} style={{ width: commentsOpen ? commentsW : 0 }}>
          {commentsOpen && <div className="resize-handle" onMouseDown={startResize} title="Drag to resize" />}
          <div className="panel-head">Comments <span className="count">{openCount}</span></div>
          <div className="panel-body">
            {view && <Margin docId={docId} content={view.content} rootRef={rootRef} comments={view.comments ?? []} onChange={refresh} />}
          </div>
        </aside>
      </div>
    </div>
  );
}
