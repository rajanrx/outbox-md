import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { listDocs, listProjects, getDoc, getConfig, approve, reapprove, type DocView, type Project } from "./api";
import { FileTree } from "./docs/FileTree";
import { Reader } from "./reader/Reader";
import { Margin } from "./comments/Margin";
import { BaselineDiff } from "./governance/BaselineDiff";
import { DecisionLog } from "./log/DecisionLog";
import { Modal } from "./Modal";
import { SettingsPanel } from "./settings/SettingsPanel";
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
const GearIcon = () => (
  <svg viewBox="0 0 18 18" fill="none" stroke="currentColor" strokeWidth="1.4">
    <circle cx="9" cy="9" r="2.4" />
    <path d="M9 1.8v2M9 14.2v2M1.8 9h2M14.2 9h2M3.9 3.9l1.4 1.4M12.7 12.7l1.4 1.4M14.1 3.9l-1.4 1.4M5.3 12.7l-1.4 1.4" strokeLinecap="round" />
  </svg>
);

// versionLabel renders the raw version string as a badge: "dev" as-is, a tag
// starting with "v" as-is, otherwise prefixed with "v".
const versionLabel = (v: string) => (v === "dev" || v.startsWith("v") ? v : `v${v}`);

const COMMENTS_W_KEY = "outbox.commentsWidth";
const clampW = (w: number) => Math.min(760, Math.max(300, w));
const TREE_W_KEY = "outbox.treeWidth";
const clampTreeW = (w: number) => Math.min(560, Math.max(180, w));
const PROJECT_KEY = "outbox.project";

export default function App() {
  const [docs, setDocs] = useState<{ id: string; path: string; project: string }[]>([]);
  const [projects, setProjects] = useState<Project[]>([]);
  // Selected project name; persisted so a reload keeps the same project. The
  // empty string is the single-folder mode (no switcher shown).
  const [project, setProject] = useState<string>(() => localStorage.getItem(PROJECT_KEY) ?? "");
  const [docId, setDocId] = useState("");
  const [view, setView] = useState<DocView | null>(null);
  const [treeOpen, setTreeOpen] = useState(true);
  const [commentsOpen, setCommentsOpen] = useState(true);
  const [showBaseline, setShowBaseline] = useState(false);
  const [showLog, setShowLog] = useState(false);
  // Which approval action is awaiting confirmation in the modal (null = closed).
  const [confirm, setConfirm] = useState<null | "approve" | "reapprove">(null);
  const [showSettings, setShowSettings] = useState(false);
  // Build version for the header badge ("dev" for local builds).
  const [version, setVersion] = useState("");
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
    if (!docId) return;
    const next = await getDoc(docId);
    // Diff before render: only replace the view when the fetched doc actually
    // changed. Returning the previous reference lets React bail out of the
    // re-render, so the fallback poll (and no-op SSE events) can't flicker the
    // reader's pane by re-setting identical content.
    setView((prev) =>
      prev && JSON.stringify(prev) === JSON.stringify(next) ? prev : next);
  }, [docId]);

  // Latest refresh + open doc behind refs so the mount-once SSE effect always
  // calls the current closure without re-subscribing (re-opening the stream) on
  // every doc switch.
  const refreshRef = useRef(refresh);
  useEffect(() => { refreshRef.current = refresh; }, [refresh]);
  const docIdRef = useRef(docId);
  useEffect(() => { docIdRef.current = docId; }, [docId]);
  // The live EventSource, held behind a ref so the fallback poll can check its
  // health (readyState) without re-subscribing to the stream.
  const esRef = useRef<EventSource | null>(null);

  // Open-thread live refresh: each Card fetches its own thread and only reloads
  // on comment.id change, so bump this on every relevant SSE event (below) and
  // thread it down to the open Card as reloadKey.
  const [threadTick, setThreadTick] = useState(0);

  // Live updates: subscribe once to the server's SSE stream. Each governance
  // event refreshes the view when it concerns the open doc (or carries no docId).
  // EventSource auto-reconnects; ": connected"/": ping" comment frames are ignored.
  useEffect(() => {
    const es = new EventSource("/api/events");
    esRef.current = es;
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
    // docs.changed is the filesystem watcher's browser-only event: a .md was
    // created/edited/deleted on disk while the server ran. Update the doc LIST
    // ONLY (new files appear, deleted ones vanish) — deliberately do NOT reload
    // the open document or its threads: a reader mid-doc must not be disturbed by
    // a background file change. Fired on the SSE hub only, never the webhook runner.
    const onDocsChanged = () => {
      listDocs().then((d) => setDocs(d ?? []));
    };
    es.addEventListener("docs.changed", onDocsChanged);
    return () => { es.close(); esRef.current = null; };
  }, []);

  useEffect(() => { listDocs().then((d) => setDocs(d ?? [])); }, []);
  useEffect(() => { getConfig().then((c) => setVersion(c?.version ?? "")); }, []);
  // Load the registered projects; keep the stored selection if it is still valid,
  // otherwise fall back to the first project (single-folder mode → empty name).
  useEffect(() => {
    listProjects().then((ps) => {
      setProjects(ps);
      setProject((cur) => (ps.some((p) => p.name === cur) ? cur : ps[0]?.name ?? ""));
    });
  }, []);
  useEffect(() => { localStorage.setItem(PROJECT_KEY, project); }, [project]);

  // Show the switcher only with 2+ projects — single-folder UX stays uncluttered.
  const multiProject = projects.length > 1;
  // Docs visible in the tree: filtered to the selected project when multi-project,
  // otherwise every doc (single-folder mode, back-compat).
  const visibleDocs = useMemo(
    () => (multiProject ? docs.filter((d) => d.project === project) : docs),
    [docs, project, multiProject],
  );

  // Pick an open document within the visible set: keep the current one if it is
  // still visible, else restore the ?doc= path, else the first visible doc.
  useEffect(() => {
    if (!visibleDocs.length) { if (docId) setDocId(""); return; }
    if (visibleDocs.some((d) => d.id === docId)) return;
    const wantPath = new URLSearchParams(window.location.search).get("doc");
    const match = wantPath ? visibleDocs.find((x) => x.path === wantPath) : undefined;
    setDocId(match ? match.id : visibleDocs[0].id);
  }, [visibleDocs, docId]);
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
    // SSE (above) is the primary live-update path; this slow poll is only a
    // fallback for a dropped stream. Gate it on SSE health: when the stream is
    // OPEN (the normal case) the interval is a no-op, so it never re-fetches and
    // never flickers. It only refreshes while the stream is down/reconnecting.
    const t = setInterval(() => {
      if (esRef.current?.readyState !== EventSource.OPEN) refresh();
    }, 15000);
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
        <div className="brand">
          <span className="dot" /><b>outbox</b><span>·md</span>
          {version && <span className="version-badge" title="outbox-md version">{versionLabel(version)}</span>}
        </div>
        {multiProject && (
          <select
            className="project-switcher"
            value={project}
            onChange={(e) => setProject(e.target.value)}
            title="Switch project"
          >
            {projects.map((p) => (
              <option key={p.name} value={p.name}>{p.name || "(root)"}</option>
            ))}
          </select>
        )}
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
        <button className="icon-btn" title="Settings" aria-label="Settings" onClick={() => setShowSettings(true)}><GearIcon /></button>
        <button className={"icon-btn" + (treeOpen ? " on" : "")} title="Toggle files" onClick={() => setTreeOpen((v) => !v)}><PanelLeftIcon /></button>
        <button className={"icon-btn" + (commentsOpen ? " on" : "")} title="Toggle comments" onClick={() => setCommentsOpen((v) => !v)}><PanelRightIcon /></button>
        {showSettings && (
          <SettingsPanel
            project={project}
            projectLabel={multiProject ? (project || "(root)") : undefined}
            onClose={() => setShowSettings(false)}
          />
        )}
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
          <div className="panel-body"><FileTree docs={visibleDocs} activeId={docId} onSelect={setDocId} /></div>
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
            {view && <Margin docId={docId} content={view.content} docPath={view.document.path} rootRef={rootRef} comments={view.comments ?? []} reloadKey={threadTick} onChange={refresh} />}
          </div>
        </aside>
      </div>
    </div>
  );
}
