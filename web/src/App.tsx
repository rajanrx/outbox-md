import { useCallback, useEffect, useRef, useState } from "react";
import { listDocs, getDoc, approve, reapprove, getConfig, type DocView } from "./api";
import { FileTree } from "./docs/FileTree";
import { Reader } from "./reader/Reader";
import { Margin } from "./comments/Margin";
import { BaselineDiff } from "./governance/BaselineDiff";
import { DecisionLog } from "./log/DecisionLog";
import { Modal } from "./Modal";
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
  // Whether the served folder is a git repo — enables the modal's folder-diff
  // view. Fetched once at startup; false is a safe default (single-file only).
  const [hasGit, setHasGit] = useState(false);
  useEffect(() => { getConfig().then((c) => setHasGit(!!c.hasGit)); }, []);
  // Which approval action is awaiting confirmation in the modal (null = closed).
  const [confirm, setConfirm] = useState<null | "approve" | "reapprove">(null);
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

  // Latest refresh + open doc behind refs so the mount-once SSE effect always
  // calls the current closure without re-subscribing (re-opening the stream) on
  // every doc switch.
  const refreshRef = useRef(refresh);
  useEffect(() => { refreshRef.current = refresh; }, [refresh]);
  const docIdRef = useRef(docId);
  useEffect(() => { docIdRef.current = docId; }, [docId]);

  // Open-thread live refresh: each Card fetches its own thread and only reloads
  // on comment.id change, so bump this on every relevant SSE event (below) and
  // thread it down to the open Card as reloadKey.
  const [threadTick, setThreadTick] = useState(0);

  // Live updates: subscribe once to the server's SSE stream. Each governance
  // event refreshes the view when it concerns the open doc (or carries no docId).
  // EventSource auto-reconnects; ": connected"/": ping" comment frames are ignored.
  useEffect(() => {
    const es = new EventSource("/api/events");
    // On (re)connect, refresh immediately — a dropped-then-restored stream may
    // have missed events, and waiting for the 15s fallback poll would lag the UI.
    es.onopen = () => { refreshRef.current?.(); setThreadTick((t) => t + 1); };
    const onEvent = (e: MessageEvent) => {
      try {
        const d = JSON.parse(e.data) as { docId?: string };
        if (!d.docId || d.docId === docIdRef.current) {
          refreshRef.current();
          // refresh() reloads the comment LIST; each open Card keeps its own
          // separately-fetched thread state, so also bump a key it depends on to
          // re-fetch the OPEN thread in place (where the agent's reply lands).
          setThreadTick((t) => t + 1);
        }
      } catch { /* ignore malformed frames */ }
    };
    // comment.updated / suggestion.proposed are AGENT-action events: the server
    // fires them to the SSE hub (browser) but NOT to the webhook runner, so the
    // UI reflects an agent reply/suggestion live without re-triggering the agent.
    for (const name of ["comment.created", "comment.replied", "comment.resolved", "document.approved", "comment.updated", "suggestion.proposed", "comment.processing"]) {
      es.addEventListener(name, onEvent);
    }
    return () => es.close();
  }, []);

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
    // SSE (above) is the primary live-update path; this slow poll is just a
    // fallback for missed/dropped events (e.g. a brief stream drop).
    const t = setInterval(refresh, 15000);
    return () => clearInterval(t);
  }, [docId, refresh]);

  const activePath = docs.find((d) => d.id === docId)?.path ?? "";
  const crumbs = activePath ? activePath.split("/") : [];
  // Approval is gated on every comment being resolved (the backend enforces it;
  // this disables the button so a blocked approve is never attempted).
  const unresolved = view?.comments?.filter((c) => c.status !== "resolved").length ?? 0;
  const openCount = unresolved;
  const gateTitle = unresolved ? `Resolve all ${unresolved} comment(s) first` : "";

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
              <button className="gov-btn" disabled={unresolved > 0} title={gateTitle} onClick={() => setConfirm("approve")}>Approve</button>
            )}
            {view.document.status === "amending" && (
              <>
                <button className="gov-btn ghost" onClick={() => setShowBaseline((v) => !v)}>View changes</button>
                <button className="gov-btn" disabled={unresolved > 0} title={gateTitle} onClick={() => setConfirm("reapprove")}>Re-approve</button>
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
        <Modal
          open={confirm !== null}
          title={confirm === "reapprove" ? "Re-approve document" : "Approve document"}
          body={
            confirm === "reapprove"
              ? "Re-approve this document? This pins the current version as the approved baseline."
              : "Approve this document? This pins the current version as the approved baseline."
          }
          confirmLabel={confirm === "reapprove" ? "Re-approve" : "Approve"}
          onCancel={() => setConfirm(null)}
          onConfirm={async () => {
            const action = confirm;
            setConfirm(null);
            if (action === "approve") await approve(docId);
            else if (action === "reapprove") { await reapprove(docId); setShowBaseline(false); }
            refresh();
          }}
        />
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
            {view && <Margin docId={docId} content={view.content} docPath={view.document.path} hasGit={hasGit} rootRef={rootRef} comments={view.comments ?? []} reloadKey={threadTick} onChange={refresh} />}
          </div>
        </aside>
      </div>
    </div>
  );
}
