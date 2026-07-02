import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { getSettings, putSettings, type Settings } from "../api";
import "./settings.css";

// SettingsPanel edits the current project's outbox.yaml through PUT /api/settings
// (comment- and unmanaged-key-preserving on the server). It loads the current
// values on open and saves the whole editable set. In multi-project mode it edits
// the selected project (its name is passed through); single-folder mode ignores it.
export function SettingsPanel({ project, projectLabel, onClose }: {
  project: string;
  projectLabel?: string;
  onClose: () => void;
}) {
  const [values, setValues] = useState<Settings | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") onClose(); };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onClose]);

  useEffect(() => {
    let live = true;
    getSettings(project).then((s) => { if (live) setValues(s); });
    return () => { live = false; };
  }, [project]);

  const set = <K extends keyof Settings>(k: K, v: Settings[K]) => {
    setSaved(false);
    setValues((prev) => (prev ? { ...prev, [k]: v } : prev));
  };

  const save = async () => {
    if (!values) return;
    setBusy(true); setError(null); setSaved(false);
    try {
      const next = await putSettings(values, project);
      setValues(next);
      setSaved(true);
    } catch (e) {
      setError(e instanceof Error ? e.message : "save failed");
    } finally {
      setBusy(false);
    }
  };

  return createPortal(
    <div className="modal-backdrop" role="presentation" onMouseDown={onClose}>
      <div
        className="modal-card settings-panel"
        role="dialog"
        aria-modal="true"
        aria-label="Settings"
        onMouseDown={(e) => e.stopPropagation()}
      >
        <h2 className="modal-title">Settings{projectLabel ? ` · ${projectLabel}` : ""}</h2>
        {values === null ? (
          <p className="modal-body">Loading…</p>
        ) : (
          <div className="settings-form">
            <label className="settings-toggle">
              <input type="checkbox" checked={values.auto_update} onChange={(e) => set("auto_update", e.target.checked)} />
              <span className="settings-copy">
                <b>Auto-update</b>
                <small>Self-update on <code>outbox up</code>.</small>
              </span>
            </label>
            <label className="settings-toggle">
              <input type="checkbox" checked={values.auto_reply} onChange={(e) => set("auto_reply", e.target.checked)} />
              <span className="settings-copy">
                <b>Auto-reply</b>
                <small>Spawn the agent CLI on each human comment.</small>
              </span>
            </label>
            <label className="settings-field">
              <span className="settings-copy">
                <b>Agent command</b>
                <small>Template the auto-reply engine spawns (the <code>{"{prompt}"}</code> token is replaced).</small>
              </span>
              <input
                type="text"
                value={values.agent_cmd}
                spellCheck={false}
                placeholder="claude -p {prompt} --allowedTools mcp__outbox-md__*"
                onChange={(e) => set("agent_cmd", e.target.value)}
              />
            </label>
            <p className="settings-note">
              These settings are read when outbox starts. <b>Restart</b> <code>outbox up</code>
              {" "}for a change (auto-reply, agent command, auto-update) to take effect on the running server.
            </p>
          </div>
        )}
        {error && <p className="settings-error" role="alert">{error}</p>}
        <div className="modal-actions">
          {saved && <span className="settings-saved">Saved ✓ · restart to apply</span>}
          <button className="gov-btn ghost" onClick={onClose}>Close</button>
          <button className="gov-btn" disabled={busy || values === null} onClick={save}>
            {busy ? "Saving…" : "Save"}
          </button>
        </div>
      </div>
    </div>,
    document.body,
  );
}
