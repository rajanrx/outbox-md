import { useEffect, useState } from "react";
import {
  createProject,
  deleteProject,
  getSettings,
  isSingleFolderMode,
  putSettings,
  updateProject,
  type Member,
  type Project,
  type Settings,
} from "../api";
import "./settings.css";

// The agent presets the backend knows how to spawn. Anything else is treated as
// a raw command (the "custom" branch of the member editor).
const PRESETS = ["claude", "codex", "copilot"] as const;

// versionLabel mirrors App.tsx: "dev"/"vX" as-is, otherwise prefixed with "v".
const versionLabel = (v: string) => (v === "dev" || v.startsWith("v") ? v : `v${v}`);

// docsToText / textToDocs convert between the docs array and the textarea's
// comma/line-separated text, trimming blanks.
const docsToText = (docs: string[]) => docs.join("\n");
const textToDocs = (text: string) =>
  text
    .split(/[\n,]/)
    .map((s) => s.trim())
    .filter(Boolean);

// projectDocs normalises the back-compat docs shapes (array | string | path) to
// a string array for editing.
function projectDocs(p: Project): string[] {
  if (Array.isArray(p.docs)) return p.docs;
  if (typeof p.docs === "string" && p.docs) return [p.docs];
  if (p.path) return [p.path];
  return ["."];
}

// SaveState is the shared save/saved/error indicator each section renders.
type SaveState = { busy: boolean; saved: boolean; error: string | null };
const idleSave: SaveState = { busy: false, saved: false, error: null };

// ── SettingsPage ────────────────────────────────────────────────────────────
// A dedicated full-page view (not a modal) that configures everything outbox
// reflects to outbox.yaml + projects.json: the projects roster, the council
// guardrails, and the automation toggles. Reached from the top-bar gear; the
// "← Back to docs" link returns to the reader.
export function SettingsPage({
  projects,
  initialProject,
  version,
  onBack,
  onProjectsChanged,
}: {
  projects: Project[];
  initialProject: string;
  version: string;
  onBack: () => void;
  onProjectsChanged: () => void;
}) {
  const singleFolder = isSingleFolderMode(projects);

  return (
    <div className="settings-page">
      <header className="settings-page-head">
        <button className="settings-back" onClick={onBack}>← Back to docs</button>
        <div className="brand">
          <span className="dot" /><b>outbox</b><span>·md</span>
          {version && <span className="version-badge" title="outbox-md version">{versionLabel(version)}</span>}
        </div>
        <div className="spacer" />
        <span className="settings-page-title">Settings</span>
      </header>

      <div className="settings-page-body">
        <ProjectsSection projects={projects} singleFolder={singleFolder} onProjectsChanged={onProjectsChanged} />
        <OutboxYamlSections projects={projects} initialProject={initialProject} singleFolder={singleFolder} />
      </div>
    </div>
  );
}

// ── Projects ─────────────────────────────────────────────────────────────────
function ProjectsSection({
  projects,
  singleFolder,
  onProjectsChanged,
}: {
  projects: Project[];
  singleFolder: boolean;
  onProjectsChanged: () => void;
}) {
  // A restart notice is surfaced after any successful projects write: the running
  // engine captures council/watcher config at startup, so a change only fully
  // takes effect after `outbox up` is restarted.
  const [restartHint, setRestartHint] = useState(false);
  const noticeChanged = () => { setRestartHint(true); onProjectsChanged(); };

  return (
    <section className="settings-section">
      <h2 className="settings-section-title">Projects</h2>
      {singleFolder ? (
        <p className="settings-note" role="note">
          Projects are managed via the CLI (<code>outbox add</code>) in single-folder mode.
        </p>
      ) : (
        <>
          {restartHint && (
            <p className="settings-note settings-restart" role="status">
              Restart <code>outbox up</code> for project changes to fully take effect — the running engine captures its
              config at startup.
            </p>
          )}
          {projects.map((p) => (
            <ProjectCard key={p.name} project={p} onChanged={noticeChanged} />
          ))}
          <AddProject onAdded={noticeChanged} />
        </>
      )}
    </section>
  );
}

// ProjectCard edits one existing project's docs/members/chair and can remove it.
function ProjectCard({ project, onChanged }: { project: Project; onChanged: () => void }) {
  const [docs, setDocs] = useState(docsToText(projectDocs(project)));
  const [members, setMembers] = useState<Member[]>(project.members ?? []);
  const [chair, setChair] = useState<Member>(project.chair ?? { agent: "", model: "" });
  const [save, setSave] = useState<SaveState>(idleSave);
  const [confirming, setConfirming] = useState(false);

  const chairRequired = members.length >= 2;
  const chairMissing = chairRequired && !chair.agent.trim();

  const doSave = async () => {
    if (chairMissing) {
      setSave({ busy: false, saved: false, error: "A chair is required when there are 2 or more members." });
      return;
    }
    setSave({ busy: true, saved: false, error: null });
    try {
      await updateProject(project.name, {
        docs: textToDocs(docs),
        members,
        chair: chair.agent.trim() ? chair : null,
      });
      setSave({ busy: false, saved: true, error: null });
      onChanged();
    } catch (e) {
      setSave({ busy: false, saved: false, error: e instanceof Error ? e.message : "save failed" });
    }
  };

  const doRemove = async () => {
    setSave({ busy: true, saved: false, error: null });
    try {
      await deleteProject(project.name);
      onChanged();
    } catch (e) {
      setSave({ busy: false, saved: false, error: e instanceof Error ? e.message : "remove failed" });
      setConfirming(false);
    }
  };

  const dirty = () => setSave((s) => (s.saved || s.error ? idleSave : s));

  return (
    <div className="settings-card">
      <div className="settings-card-head">
        <div className="settings-copy">
          <b>{project.name || "(root)"}</b>
          <small className="settings-root-path">{project.root}</small>
        </div>
        {confirming ? (
          <span className="settings-confirm">
            Remove <b>{project.name}</b>?
            <button className="gov-btn ghost" onClick={() => setConfirming(false)}>Cancel</button>
            <button className="gov-btn danger" disabled={save.busy} onClick={doRemove}>Remove</button>
          </span>
        ) : (
          <button className="gov-btn ghost" onClick={() => setConfirming(true)}>Remove</button>
        )}
      </div>

      <label className="settings-field">
        <span className="settings-copy"><b>Docs</b><small>Served spec subpaths — one per line or comma-separated.</small></span>
        <textarea
          className="settings-textarea"
          rows={2}
          value={docs}
          spellCheck={false}
          onChange={(e) => { setDocs(e.target.value); dirty(); }}
        />
      </label>

      <MembersEditor
        members={members}
        chair={chair}
        onMembers={(m) => { setMembers(m); dirty(); }}
        onChair={(c) => { setChair(c); dirty(); }}
        chairMissing={chairMissing}
      />

      <SaveRow state={save} onSave={doSave} savedLabel="Saved ✓ · restart to apply" />
    </div>
  );
}

// AddProject is the "register a new project" form → POST /api/projects.
function AddProject({ onAdded }: { onAdded: () => void }) {
  const [open, setOpen] = useState(false);
  const [root, setRoot] = useState("");
  const [docs, setDocs] = useState("");
  const [members, setMembers] = useState<Member[]>([]);
  const [chair, setChair] = useState<Member>({ agent: "", model: "" });
  const [save, setSave] = useState<SaveState>(idleSave);

  const reset = () => {
    setRoot(""); setDocs(""); setMembers([]); setChair({ agent: "", model: "" }); setSave(idleSave);
  };

  const chairRequired = members.length >= 2;
  const chairMissing = chairRequired && !chair.agent.trim();

  const submit = async () => {
    if (!root.trim()) { setSave({ busy: false, saved: false, error: "A project root path is required." }); return; }
    if (chairMissing) { setSave({ busy: false, saved: false, error: "A chair is required when there are 2 or more members." }); return; }
    setSave({ busy: true, saved: false, error: null });
    try {
      await createProject({
        root: root.trim(),
        docs: textToDocs(docs),
        members,
        chair: chair.agent.trim() ? chair : null,
      });
      onAdded();
      setOpen(false);
      reset();
    } catch (e) {
      setSave({ busy: false, saved: false, error: e instanceof Error ? e.message : "add failed" });
    }
  };

  if (!open) {
    return <button className="gov-btn settings-add-btn" onClick={() => setOpen(true)}>+ Add project</button>;
  }
  return (
    <div className="settings-card settings-card-new">
      <div className="settings-card-head"><b>New project</b></div>
      <label className="settings-field">
        <span className="settings-copy"><b>Root path</b><small>Absolute or relative path to the repo root.</small></span>
        <input type="text" value={root} spellCheck={false} placeholder="/path/to/repo" onChange={(e) => setRoot(e.target.value)} />
      </label>
      <label className="settings-field">
        <span className="settings-copy"><b>Docs</b><small>Served spec subpaths — one per line or comma-separated.</small></span>
        <textarea className="settings-textarea" rows={2} value={docs} spellCheck={false} placeholder="docs/specs" onChange={(e) => setDocs(e.target.value)} />
      </label>
      <MembersEditor
        members={members}
        chair={chair}
        onMembers={setMembers}
        onChair={setChair}
        chairMissing={chairMissing}
      />
      {save.error && <p className="settings-error" role="alert">{save.error}</p>}
      <div className="modal-actions">
        <button className="gov-btn ghost" onClick={() => { setOpen(false); reset(); }}>Cancel</button>
        <button className="gov-btn" disabled={save.busy} onClick={submit}>{save.busy ? "Adding…" : "Add project"}</button>
      </div>
    </div>
  );
}

// ── Members editor (shared by ProjectCard + AddProject) ──────────────────────
function MembersEditor({
  members,
  chair,
  onMembers,
  onChair,
  chairMissing,
}: {
  members: Member[];
  chair: Member;
  onMembers: (m: Member[]) => void;
  onChair: (c: Member) => void;
  chairMissing: boolean;
}) {
  const setMember = (i: number, next: Member) => onMembers(members.map((m, j) => (j === i ? next : m)));
  const addMember = () => onMembers([...members, { agent: "claude", model: "" }]);
  const removeMember = (i: number) => onMembers(members.filter((_, j) => j !== i));

  return (
    <div className="settings-members">
      <div className="settings-copy"><b>Council members</b><small>Each reviews with the given agent + optional model.</small></div>
      {members.length === 0 && <p className="settings-empty">No members — solo mode.</p>}
      {members.map((m, i) => (
        <MemberRow key={i} member={m} onChange={(n) => setMember(i, n)} onRemove={() => removeMember(i)} />
      ))}
      <button className="gov-btn ghost settings-add-member" onClick={addMember}>+ Add member</button>

      {members.length >= 2 && (
        <div className="settings-chair">
          <div className="settings-copy">
            <b>Chair {chairMissing && <span className="settings-req">· required</span>}</b>
            <small>Breaks ties across ≥2 members.</small>
          </div>
          <MemberRow member={chair} onChange={onChair} invalid={chairMissing} />
        </div>
      )}
    </div>
  );
}

// MemberRow is a single {agent, model} editor: a preset dropdown, a raw-command
// text field when "custom", and an optional model field. onRemove is omitted for
// the chair (which is cleared by blanking its agent, not removed).
function MemberRow({
  member,
  onChange,
  onRemove,
  invalid,
}: {
  member: Member;
  onChange: (m: Member) => void;
  onRemove?: () => void;
  invalid?: boolean;
}) {
  const isPreset = (PRESETS as readonly string[]).includes(member.agent);
  // "" agent starts as a preset (claude) selection; a non-empty non-preset value
  // is an existing custom command.
  const custom = member.agent !== "" && !isPreset;
  const [mode, setMode] = useState<"preset" | "custom">(custom ? "custom" : "preset");

  const onSelect = (v: string) => {
    if (v === "custom") { setMode("custom"); onChange({ ...member, agent: "" }); return; }
    setMode("preset");
    onChange({ ...member, agent: v });
  };

  return (
    <div className={"settings-member-row" + (invalid ? " invalid" : "")}>
      <select
        className="settings-agent-select"
        aria-label="Agent preset"
        value={mode === "custom" ? "custom" : member.agent || "claude"}
        onChange={(e) => onSelect(e.target.value)}
      >
        {PRESETS.map((p) => <option key={p} value={p}>{p}</option>)}
        <option value="custom">custom command…</option>
      </select>
      {mode === "custom" && (
        <input
          type="text"
          className="settings-agent-cmd"
          aria-label="Custom command"
          value={member.agent}
          spellCheck={false}
          placeholder="my-agent -p {prompt}"
          onChange={(e) => onChange({ ...member, agent: e.target.value })}
        />
      )}
      <input
        type="text"
        className="settings-model"
        aria-label="Model"
        value={member.model ?? ""}
        spellCheck={false}
        placeholder="model (optional)"
        onChange={(e) => onChange({ ...member, model: e.target.value })}
      />
      {onRemove && <button className="settings-member-x" aria-label="Remove member" onClick={onRemove}>×</button>}
    </div>
  );
}

// ── Council + Automation (outbox.yaml) ───────────────────────────────────────
// Both edit the same per-project outbox.yaml via GET/PUT /api/settings. In
// multi-project mode a small selector chooses which project's file to edit;
// single-folder mode edits the one folder.
function OutboxYamlSections({
  projects,
  initialProject,
  singleFolder,
}: {
  projects: Project[];
  initialProject: string;
  singleFolder: boolean;
}) {
  const multi = !singleFolder && projects.length > 1;
  const [proj, setProj] = useState(initialProject);
  const [values, setValues] = useState<Settings | null>(null);
  const [council, setCouncil] = useState<SaveState>(idleSave);
  const [automation, setAutomation] = useState<SaveState>(idleSave);

  useEffect(() => {
    let live = true;
    setValues(null);
    getSettings(proj).then((s) => { if (live) setValues(s); });
    return () => { live = false; };
  }, [proj]);

  const set = <K extends keyof Settings>(k: K, v: Settings[K]) => {
    setValues((prev) => (prev ? { ...prev, [k]: v } : prev));
    setCouncil((s) => (s.saved || s.error ? idleSave : s));
    setAutomation((s) => (s.saved || s.error ? idleSave : s));
  };

  // saveKeys writes only the given subset of keys (PUT is a partial merge — an
  // unknown/absent key is untouched), so each section saves independently.
  const saveKeys = async (keys: (keyof Settings)[], setState: (s: SaveState) => void) => {
    if (!values) return;
    setState({ busy: true, saved: false, error: null });
    try {
      const patch: Partial<Settings> = {};
      for (const k of keys) (patch as Record<string, unknown>)[k] = values[k];
      const next = await putSettings(patch, proj);
      setValues(next);
      setState({ busy: false, saved: true, error: null });
    } catch (e) {
      setState({ busy: false, saved: false, error: e instanceof Error ? e.message : "save failed" });
    }
  };

  const num = (v: string) => {
    const n = Number(v);
    return Number.isFinite(n) ? Math.trunc(n) : 0;
  };

  return (
    <>
      {multi && (
        <div className="settings-proj-picker">
          <label>Editing outbox.yaml for
            <select value={proj} onChange={(e) => setProj(e.target.value)}>
              {projects.map((p) => <option key={p.name} value={p.name}>{p.name || "(root)"}</option>)}
            </select>
          </label>
        </div>
      )}

      <section className="settings-section">
        <h2 className="settings-section-title">Council</h2>
        {values === null ? <p className="settings-empty">Loading…</p> : (
          <div className="settings-grid">
            <label className="settings-field">
              <span className="settings-copy"><b>Rounds</b><small>Max review rounds before the chair decides.</small></span>
              <input type="number" min={0} value={values.council_rounds} onChange={(e) => set("council_rounds", num(e.target.value))} />
            </label>
            <label className="settings-field">
              <span className="settings-copy"><b>Budget</b><small>Token/step budget per council run.</small></span>
              <input type="number" min={0} value={values.council_budget} onChange={(e) => set("council_budget", num(e.target.value))} />
            </label>
            <label className="settings-field">
              <span className="settings-copy"><b>Deadlock threshold</b><small>Rounds without progress before forcing a decision.</small></span>
              <input type="number" min={0} value={values.council_deadlock_threshold} onChange={(e) => set("council_deadlock_threshold", num(e.target.value))} />
            </label>
          </div>
        )}
        <SaveRow state={council} onSave={() => saveKeys(["council_rounds", "council_budget", "council_deadlock_threshold"], setCouncil)} disabled={values === null} savedLabel="Saved ✓ · restart to apply" />
      </section>

      <section className="settings-section">
        <h2 className="settings-section-title">Automation</h2>
        {values === null ? <p className="settings-empty">Loading…</p> : (
          <div className="settings-form">
            <label className="settings-toggle">
              <input type="checkbox" checked={values.auto_reply} onChange={(e) => set("auto_reply", e.target.checked)} />
              <span className="settings-copy"><b>Auto-reply</b><small>Spawn the agent CLI on each human comment.</small></span>
            </label>
            <label className="settings-toggle">
              <input type="checkbox" checked={values.auto_update} onChange={(e) => set("auto_update", e.target.checked)} />
              <span className="settings-copy"><b>Auto-update</b><small>Self-update on <code>outbox up</code>.</small></span>
            </label>
            <label className="settings-field">
              <span className="settings-copy"><b>Agent command</b><small>Template the auto-reply engine spawns (the <code>{"{prompt}"}</code> token is replaced).</small></span>
              <input type="text" value={values.agent_cmd} spellCheck={false} placeholder="claude -p {prompt} --allowedTools mcp__outbox-md__*" onChange={(e) => set("agent_cmd", e.target.value)} />
            </label>
          </div>
        )}
        <SaveRow state={automation} onSave={() => saveKeys(["auto_reply", "auto_update", "agent_cmd"], setAutomation)} disabled={values === null} savedLabel="Saved ✓ · restart to apply" />
      </section>
    </>
  );
}

// SaveRow renders the shared save/saved/error footer for a section.
function SaveRow({
  state,
  onSave,
  disabled,
  savedLabel = "Saved ✓",
}: {
  state: SaveState;
  onSave: () => void;
  disabled?: boolean;
  savedLabel?: string;
}) {
  return (
    <>
      {state.error && <p className="settings-error" role="alert">{state.error}</p>}
      <div className="modal-actions">
        {state.saved && <span className="settings-saved">{savedLabel}</span>}
        <button className="gov-btn" disabled={state.busy || disabled} onClick={onSave}>
          {state.busy ? "Saving…" : "Save"}
        </button>
      </div>
    </>
  );
}
