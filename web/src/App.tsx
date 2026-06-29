import { useCallback, useEffect, useRef, useState } from "react";
import { listDocs, getDoc, approve, reapprove, type DocView } from "./api";
import { FileTree } from "./docs/FileTree";
import { Reader } from "./reader/Reader";
import { Margin } from "./comments/Margin";
import { BaselineDiff } from "./governance/BaselineDiff";
import { DecisionLog } from "./log/DecisionLog";
import "./governance/governance.css";

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
const TREE_W_KEY = "outbox.treeWidth";
const clampTreeW = (w: number) => Math.min(560, Math.max(180, w));

export default function App() {
  const [docs, setDocs] = useState<{ id: string; path: string }[]>([]);
  const [docId, setDocId] = useState("");
  const [view, setView] = useState<DocView | null>(null);
  const [treeOpen, setTreeOpen] = useState(true);
  const [commentsOpen, setCommentsOpen] = useState(true);
  const [showBaseline, setShowBaseline] = useState(false);
  const [showLog, setShowLog] = useState(false);
  const [commentsW, setCommentsW] = useState(() => {
    const v = Number(localStorage.getItem(COMMENTS_W_KEY));
    return v ? clampW(v) : 420;
  });
  const [treeW, setTreeW] = useState(() => {
    const v = Number(localStorage.getItem(TREE_W_KEY));
    return v ? clampTreeW(v) : 270;
  });
  const rootRef = useRef<HTMLDivElement>(null);
  const docReflectedRef = useRef(false);

  useEffect(() => { localStorage.setItem(COMMENTS_W_KEY, String(commentsW)); }, [commentsW]);
  useEffect(() => { localStorage.setItem(TREE_W_KEY, String(treeW)); }, [treeW]);

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

  const startTreeResize = (e: React.MouseEvent) => {
    e.preventDefault();
    const onMove = (ev: MouseEvent) => setTreeW(clampTreeW(ev.clientX));
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

  useEffect(() => {
    listDocs().then((d) => {
      const list = d ?? [];
      setDocs(list);
      if (!list.length) return;
      const wantPath = new URLSearchParams(window.location.search).get("doc");
      const match = wantPath ? list.find((x) => x.path === wantPath) : undefined;
      setDocId(match ? match.id : list[0].id);
    });
  }, []);
  // Reflect the open document in the URL (?doc=<path>) so refresh restores it.
  // replaceState (not pushState) to avoid history-stack spam.
  useEffect(() => {
    if (!docId) return;
    const path = docs.find((d) => d.id === docId)?.path;
    if (!path) return;
    // The first run restores from the URL — keep its hash (the section to scroll
    // to). Every later run is a user-driven doc switch, so drop the stale section
    // hash (it belonged to the previously-open file).
    const keepHash = !docReflectedRef.current;
    docReflectedRef.current = true;
    if (new URLSearchParams(window.location.search).get("doc") === path && keepHash) return;
    const enc = encodeURIComponent(path).replace(/%2F/g, "/");
    const hash = keepHash ? window.location.hash : "";
    window.history.replaceState(null, "", `${window.location.pathname}?doc=${enc}${hash}`);
  }, [docId, docs]);
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
      <div className="topbar" style={{ position: "relative" }}>
        <div className="brand"><span className="dot" /><b>outbox</b><span>·md</span></div>
        {crumbs.length > 0 && (
          <div className="crumbs">
            {crumbs.slice(0, -1).map((c, i) => (
              <span key={i} style={{ display: "contents" }}>{c}<span className="sep">/</span></span>
            ))}
            <span className="leaf">{crumbs[crumbs.length - 1]}</span>
          </div>
        )}
        {view && (
          <div className="lifecycle-controls" style={{ display: "flex", alignItems: "center", gap: 10 }}>
            <span className={`lifecycle ${view.document.status}`}>{view.document.status}</span>
            {view.document.status === "draft" && (
              <button className="gov-btn" onClick={async () => { await approve(docId); refresh(); }}>Approve</button>
            )}
            {view.document.status === "amending" && (
              <>
                <button className="gov-btn ghost" onClick={() => setShowBaseline((v) => !v)}>View changes</button>
                <button className="gov-btn" onClick={async () => { await reapprove(docId); setShowBaseline(false); refresh(); }}>Re-approve</button>
              </>
            )}
          </div>
        )}
        {view && (
          <button className="gov-btn ghost" onClick={() => setShowLog((v) => !v)}>History</button>
        )}
        <div className="spacer" />
        <button className={"icon-btn" + (treeOpen ? " on" : "")} title="Toggle files" onClick={() => setTreeOpen((v) => !v)}><PanelLeftIcon /></button>
        <button className={"icon-btn" + (commentsOpen ? " on" : "")} title="Toggle comments" onClick={() => setCommentsOpen((v) => !v)}><PanelRightIcon /></button>
        {showBaseline && view && (
          <BaselineDiff baseline={view.baselineContent} current={view.content} onClose={() => setShowBaseline(false)} />
        )}
        {showLog && view && (
          <DecisionLog docId={docId} onClose={() => setShowLog(false)} />
        )}
      </div>

      <div className="workbench">
        <aside className={"tree-panel" + (treeOpen ? "" : " collapsed")} style={{ width: treeOpen ? treeW : 0 }}>
          {treeOpen && <div className="tree-resize-handle" onMouseDown={startTreeResize} title="Drag to resize" />}
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
