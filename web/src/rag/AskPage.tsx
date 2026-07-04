import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  askKB,
  createSession,
  deleteSession,
  listMessages,
  listSessions,
  renameSession,
  type Project,
  type RagMessage,
  type RagSession,
  type RagSource,
} from "../api";
import { MarkdownView } from "../reader/MarkdownView";
import "./rag.css";

// versionLabel mirrors App/SettingsPage: "dev"/"vX" as-is, else prefixed "v".
const versionLabel = (v: string) => (v === "dev" || v.startsWith("v") ? v : `v${v}`);

// retrievalNote turns the /api/config retrievalMode into a short human note so the
// user knows whether answers are semantically grounded or keyword-only. "semantic"
// (or unknown) → the healthy default, no note.
function retrievalNote(mode: string): { label: string; title: string } | null {
  if (mode === "keyword-only")
    return { label: "keyword search", title: "No embedder configured — retrieval is keyword-only (FTS5)" };
  if (mode === "degraded")
    return { label: "retrieval degraded", title: "Embedder unavailable — retrieval degraded to keyword search" };
  return null; // semantic / unwired → quiet
}

// AskPage is the dedicated "Ask the KB" surface: a sessions sidebar + a chat thread
// + an input box, answered by the project's LLM grounded in retrieved docs. Reached
// from the top-bar spark icon (?ask); "← Back to docs" returns to the reader.
export function AskPage({
  projects,
  initialProject,
  version,
  retrievalMode,
  onBack,
  onOpenDoc,
}: {
  projects: Project[];
  initialProject: string;
  version: string;
  retrievalMode: string;
  onBack: () => void;
  // onOpenDoc navigates back to the reader at a specific doc path (from a source).
  onOpenDoc: (path: string) => void;
}) {
  const multiProject = projects.length > 1;
  const [project, setProject] = useState(initialProject);
  const [sessions, setSessions] = useState<RagSession[]>([]);
  const [activeId, setActiveId] = useState("");
  const [messages, setMessages] = useState<RagMessage[]>([]);
  const [question, setQuestion] = useState("");
  const [thinking, setThinking] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const threadRef = useRef<HTMLDivElement>(null);

  const note = useMemo(() => retrievalNote(retrievalMode), [retrievalMode]);

  const loadSessions = useCallback(async () => {
    const s = await listSessions(project);
    setSessions(s);
    setActiveId((cur) => (s.some((x) => x.id === cur) ? cur : s[0]?.id ?? ""));
  }, [project]);
  useEffect(() => { loadSessions(); }, [loadSessions]);

  // Load the active session's messages whenever it changes.
  useEffect(() => {
    if (!activeId) { setMessages([]); return; }
    let live = true;
    listMessages(activeId).then((m) => { if (live) setMessages(m); });
    return () => { live = false; };
  }, [activeId]);

  // Keep the thread scrolled to the latest turn. scrollTo is absent in jsdom, so
  // guard it (and set scrollTop as a portable fallback).
  useEffect(() => {
    const el = threadRef.current;
    if (!el) return;
    if (typeof el.scrollTo === "function") el.scrollTo({ top: el.scrollHeight });
    else el.scrollTop = el.scrollHeight;
  }, [messages, thinking]);

  const newChat = useCallback(async () => {
    setError(null);
    try {
      const s = await createSession(project);
      setSessions((cur) => [s, ...cur]);
      setActiveId(s.id);
      setMessages([]);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, [project]);

  const rename = useCallback(async (id: string, title: string) => {
    const trimmed = title.trim();
    if (!trimmed) return;
    await renameSession(id, trimmed);
    setSessions((cur) => cur.map((s) => (s.id === id ? { ...s, title: trimmed } : s)));
  }, []);

  const remove = useCallback(async (id: string) => {
    await deleteSession(id);
    setSessions((cur) => cur.filter((s) => s.id !== id));
    setActiveId((cur) => (cur === id ? "" : cur));
  }, []);

  const send = useCallback(async () => {
    const q = question.trim();
    if (!q || thinking) return;
    setError(null);
    let sessionId = activeId;
    // No session yet → open one implicitly so the first question always lands.
    if (!sessionId) {
      try {
        const s = await createSession(project);
        setSessions((cur) => [s, ...cur]);
        setActiveId(s.id);
        sessionId = s.id;
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
        return;
      }
    }
    // Optimistically show the user's question while the answer is pending.
    const optimistic: RagMessage = {
      id: `pending-${Date.now()}`, sessionId, role: "user", content: q, sources: [], createdAt: "",
    };
    setMessages((cur) => [...cur, optimistic]);
    setQuestion("");
    setThinking(true);
    try {
      await askKB(sessionId, q);
      // Re-fetch the canonical turns (the server persisted both) so ids/order are real.
      setMessages(await listMessages(sessionId));
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      // Drop the optimistic turn — nothing was persisted on failure.
      setMessages((cur) => cur.filter((m) => m.id !== optimistic.id));
    } finally {
      setThinking(false);
    }
  }, [question, thinking, activeId, project]);

  const onKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      void send();
    }
  };

  return (
    <div className="ask-page">
      <header className="settings-page-head">
        <button className="settings-back" onClick={onBack}>← Back to docs</button>
        <div className="brand">
          <span className="dot" /><b>outbox</b><span>·md</span>
          {version && <span className="version-badge" title="outbox-md version">{versionLabel(version)}</span>}
        </div>
        {multiProject && (
          <select
            className="project-switcher"
            value={project}
            onChange={(e) => setProject(e.target.value)}
            title="Knowledge base + LLM to ask"
          >
            {projects.map((p) => (
              <option key={p.name} value={p.name}>{p.name || "(root)"}</option>
            ))}
          </select>
        )}
        <div className="spacer" />
        <span className="settings-page-title">Ask the KB</span>
      </header>

      <div className="ask-body">
        <aside className="ask-sidebar">
          <button className="ask-new" onClick={newChat}>+ New chat</button>
          <div className="ask-session-list">
            {sessions.length === 0 && <div className="ask-empty">No chats yet.</div>}
            {sessions.map((s) => (
              <SessionRow
                key={s.id}
                session={s}
                active={s.id === activeId}
                onSelect={() => setActiveId(s.id)}
                onRename={(title) => rename(s.id, title)}
                onDelete={() => remove(s.id)}
              />
            ))}
          </div>
        </aside>

        <main className="ask-main">
          <div className="ask-thread" ref={threadRef}>
            {messages.length === 0 && !thinking && (
              <div className="ask-welcome">
                <h2>Ask the knowledge base</h2>
                <p>
                  Questions are answered by the project’s LLM, grounded in the retrieved docs.
                  {note && (
                    <> Retrieval is currently <b title={note.title}>{note.label}</b>.</>
                  )}
                </p>
              </div>
            )}
            {messages.map((m) => (
              <Bubble key={m.id} message={m} onOpenDoc={onOpenDoc} />
            ))}
            {thinking && (
              <div className="ask-bubble assistant">
                <div className="ask-role">assistant</div>
                <div className="ask-thinking"><span /><span /><span /> thinking…</div>
              </div>
            )}
          </div>

          {error && <div className="ask-error" role="alert">{error}</div>}

          <div className="ask-input">
            <textarea
              value={question}
              onChange={(e) => setQuestion(e.target.value)}
              onKeyDown={onKeyDown}
              placeholder="Ask a question…  (Enter to send, Shift+Enter for a new line)"
              rows={2}
              aria-label="Ask a question"
            />
            <button className="ask-send" onClick={() => void send()} disabled={thinking || !question.trim()}>
              {thinking ? "…" : "Send"}
            </button>
          </div>
        </main>
      </div>
    </div>
  );
}

// SessionRow is one entry in the sessions sidebar: select, inline-rename (double
// click / pencil), and delete.
function SessionRow({
  session,
  active,
  onSelect,
  onRename,
  onDelete,
}: {
  session: RagSession;
  active: boolean;
  onSelect: () => void;
  onRename: (title: string) => void;
  onDelete: () => void;
}) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(session.title);
  useEffect(() => setDraft(session.title), [session.title]);

  if (editing) {
    return (
      <div className={"ask-session" + (active ? " active" : "")}>
        <input
          className="ask-session-edit"
          value={draft}
          autoFocus
          onChange={(e) => setDraft(e.target.value)}
          onBlur={() => { setEditing(false); onRename(draft); }}
          onKeyDown={(e) => {
            if (e.key === "Enter") { e.preventDefault(); setEditing(false); onRename(draft); }
            if (e.key === "Escape") { setDraft(session.title); setEditing(false); }
          }}
          aria-label="Rename chat"
        />
      </div>
    );
  }
  return (
    <div className={"ask-session" + (active ? " active" : "")} onClick={onSelect}>
      <span className="ask-session-title" title={session.title} onDoubleClick={() => setEditing(true)}>
        {session.title || "Untitled"}
      </span>
      <span className="ask-session-actions">
        <button title="Rename" aria-label="Rename chat" onClick={(e) => { e.stopPropagation(); setEditing(true); }}>✎</button>
        <button title="Delete" aria-label="Delete chat" onClick={(e) => { e.stopPropagation(); onDelete(); }}>✕</button>
      </span>
    </div>
  );
}

// Bubble renders one turn. The user turn is plain text; the assistant turn renders
// its answer as Markdown and lists the cited sources beneath.
function Bubble({ message, onOpenDoc }: { message: RagMessage; onOpenDoc: (path: string) => void }) {
  const isUser = message.role === "user";
  return (
    <div className={"ask-bubble " + message.role}>
      <div className="ask-role">{message.role}</div>
      <div className="ask-content">
        {isUser ? <p className="ask-user-text">{message.content}</p> : <MarkdownView content={message.content} />}
      </div>
      {!isUser && message.sources && message.sources.length > 0 && (
        <Sources sources={message.sources} onOpenDoc={onOpenDoc} />
      )}
    </div>
  );
}

// Sources is the compact citation list under an assistant answer: each unique doc
// path is a button that navigates back to the reader at that doc.
function Sources({ sources, onOpenDoc }: { sources: RagSource[]; onOpenDoc: (path: string) => void }) {
  // De-dupe by doc path (several passages can share a doc), preserving order.
  const seen = new Set<string>();
  const unique = sources.filter((s) => (seen.has(s.DocPath) ? false : (seen.add(s.DocPath), true)));
  return (
    <div className="ask-sources">
      <span className="ask-sources-label">Sources</span>
      {unique.map((s) => (
        <button
          key={s.DocPath}
          className="ask-source"
          title={s.Heading ? `${s.DocPath} › ${s.Heading}` : s.DocPath}
          onClick={() => onOpenDoc(s.DocPath)}
        >
          {s.DocPath}
        </button>
      ))}
    </div>
  );
}
