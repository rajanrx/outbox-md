import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import mermaid from "mermaid";

mermaid.initialize({ startOnLoad: false, theme: "default", securityLevel: "strict" });

// MermaidBlock renders a mermaid fence to SVG and adds a small hover toolbar:
//  • </> Show code — swap the rendered diagram for its mermaid source (selectable
//    <pre><code> so it can be read / copied / commented on), and back.
//  • ⤢ Fullscreen — blow the SAME rendered SVG up in a portal overlay (Esc or
//    backdrop-click to close). We reuse the already-rendered svg string rather
//    than re-rendering with the same id (a second mermaid.render(id) would clash).
// The outer .mermaid-block keeps its data-mermaid attribute — the anchor/comment
// system treats the whole block as one commentable unit via that hook.
export function MermaidBlock({ chart }: { chart: string }) {
  const [svg, setSvg] = useState<string>("");
  const [errored, setErrored] = useState(false);
  const [showCode, setShowCode] = useState(false);
  const [full, setFull] = useState(false);
  const id = useRef("m" + Math.random().toString(36).slice(2)).current;

  useEffect(() => {
    let alive = true;
    mermaid
      .render(id, chart)
      .then((r) => {
        if (!alive) return;
        setSvg(r.svg);
        setErrored(false);
      })
      .catch(() => {
        if (!alive) return;
        setSvg("");
        setErrored(true);
      });
    return () => {
      alive = false;
    };
  }, [chart, id]);

  // Esc closes ONLY the fullscreen overlay. Capture phase + stopImmediatePropagation
  // so a host modal's own document-level Escape→close handler doesn't ALSO fire —
  // otherwise one Esc would both exit fullscreen and close the surrounding modal.
  useEffect(() => {
    if (!full) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      e.stopImmediatePropagation();
      e.preventDefault();
      setFull(false);
    };
    document.addEventListener("keydown", onKey, true);
    return () => document.removeEventListener("keydown", onKey, true);
  }, [full]);

  const canFull = !errored && svg !== "";

  return (
    <div className="mermaid-block" data-mermaid>
      <div className="mermaid-toolbar" role="toolbar" aria-label="Diagram tools">
        <button
          type="button"
          className={"mermaid-tool" + (showCode ? " on" : "")}
          aria-pressed={showCode}
          title={showCode ? "Show diagram" : "Show mermaid source"}
          aria-label={showCode ? "Show diagram" : "Show mermaid source"}
          onClick={() => setShowCode((s) => !s)}
        >
          {showCode ? "▦" : "</>"}
        </button>
        <button
          type="button"
          className="mermaid-tool"
          title="Fullscreen"
          aria-label="Fullscreen diagram"
          disabled={!canFull}
          onClick={() => setFull(true)}
        >
          ⤢
        </button>
      </div>

      {showCode ? (
        <pre className="mermaid-source">
          <code>{chart}</code>
        </pre>
      ) : errored ? (
        <pre className="mermaid-error">mermaid error</pre>
      ) : (
        <div className="mermaid-render" dangerouslySetInnerHTML={{ __html: svg }} />
      )}

      {full &&
        canFull &&
        createPortal(
          <div className="mermaid-fs-backdrop" role="presentation" onMouseDown={() => setFull(false)}>
            <div
              className="mermaid-fs"
              role="dialog"
              aria-modal="true"
              aria-label="Diagram (fullscreen)"
              onMouseDown={(e) => e.stopPropagation()}
            >
              <button
                type="button"
                className="mermaid-fs-close ic-btn"
                aria-label="Close"
                title="Close"
                onClick={() => setFull(false)}
              >
                ✕
              </button>
              <div className="mermaid-fs-body" dangerouslySetInnerHTML={{ __html: svg }} />
            </div>
          </div>,
          document.body,
        )}
    </div>
  );
}
