import { useEffect } from "react";
import { createPortal } from "react-dom";
import type { Candidate, CouncilView, DiscussionMessage } from "../api";
import { authorOf, chosenVerdict, groupByRound } from "./council";

// Human labels for the three verdicts.
const VERDICT_LABEL: Record<string, string> = {
  edit: "Edit",
  reply: "Reply",
  reject_comment: "Reject comment",
};
const verdictLabel = (v: string) => VERDICT_LABEL[v] ?? v;
const initial = (who: string) => (who?.[0] || "?").toUpperCase();
const roundLabel = (r: number) => (r === 0 ? "Round 0 · Independent takes" : `Round ${r} · Discussion`);

// One member's take within a round. Attributed (identity + lens), verdict shown,
// dissent (verdict differs from the consensus) highlighted, and an edit candidate's
// proposed content shown collapsed/bounded.
function CandidateEntry({ c, dissenting }: { c: Candidate; dissenting: boolean }) {
  return (
    <div className={"council-entry" + (dissenting ? " council-dissent" : "")}>
      <span className={`avatar who-${c.agentIdentity}`}>{initial(c.agentIdentity)}</span>
      <div className="council-entry-main">
        <div className="council-entry-head">
          <span className="council-who">{c.agentIdentity}</span>
          {c.lens && <span className="council-lens">{c.lens}</span>}
          <span className={`council-verdict verdict-${c.verdict}`}>{verdictLabel(c.verdict)}</span>
          {c.chosen && <span className="council-chosen">picked</span>}
          {dissenting && <span className="council-dissent-tag">dissent</span>}
        </div>
        {c.rationale && <div className="council-rationale">{c.rationale}</div>}
        {c.verdict === "edit" && c.content && (
          <details className="council-proposed">
            <summary>Proposed content</summary>
            <pre className="council-proposed-body">{c.content}</pre>
          </details>
        )}
      </div>
    </div>
  );
}

// One debate message within a round: attributed body plus its citations
// ("responding to <member>" / a KB source).
function MessageEntry({ view, m }: { view: CouncilView; m: DiscussionMessage }) {
  return (
    <div className="council-entry council-message">
      <span className={`avatar who-${m.agentIdentity}`}>{initial(m.agentIdentity)}</span>
      <div className="council-entry-main">
        <div className="council-entry-head">
          <span className="council-who">{m.agentIdentity}</span>
        </div>
        {m.body && <div className="council-msg-body">{m.body}</div>}
        {m.refs?.length > 0 && (
          <div className="council-refs">
            {m.refs.map((ref, i) => (
              <span key={i} className="council-ref">
                {ref.kind === "kb" ? `KB: ${ref.targetId}` : `responding to ${authorOf(view, ref.targetId)}`}
              </span>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

// CouncilModal is the read-only near-full-screen view of a comment's council
// debate. It reuses the DiffModal shell (backdrop + modal-card diff-modal + pinned
// head/foot). Candidates and discussion messages are grouped by round; the chair's
// synthesis (agreement + confidence + dissent) closes it out.
export function CouncilModal({ view, title, onClose }: {
  view: CouncilView;
  title: string;
  onClose: () => void;
}) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") onClose(); };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onClose]);

  const groups = groupByRound(view);
  const consensus = chosenVerdict(view);
  const s = view.synthesis;

  return createPortal(
    <div className="modal-backdrop" role="presentation" onMouseDown={onClose}>
      <div
        className="modal-card diff-modal council-modal"
        role="dialog"
        aria-modal="true"
        aria-label={`Council discussion for ${title}`}
        onMouseDown={(e) => e.stopPropagation()}
      >
        <div className="diff-modal-head">
          <h2 className="modal-title" title={title}>Council · {title}</h2>
          <div className="diff-modal-head-tools">
            <button className="ic-btn" aria-label="Close" title="Close" onClick={onClose}>✕</button>
          </div>
        </div>

        <div className="diff-modal-body">
          {groups.map((g) => (
            <section key={g.round} className="council-round">
              <div className="council-round-title">{roundLabel(g.round)}</div>
              {g.candidates.map((c) => (
                <CandidateEntry key={c.id} c={c} dissenting={!!consensus && c.verdict !== consensus} />
              ))}
              {g.messages.map((m) => (
                <MessageEntry key={m.id} view={view} m={m} />
              ))}
            </section>
          ))}

          {s && (
            <section className="council-synthesis">
              <div className="council-round-title">Chair synthesis</div>
              <div className="council-synthesis-decision">
                {s.suggestionId ? "Edit proposed" : "Reply — no edit"}
              </div>
              {s.summary && (
                <div className="council-synthesis-summary council-msg-body">{s.summary}</div>
              )}
              <div className="council-synthesis-stats">
                <span className="council-stat">
                  Agreement <b>{Math.round(s.agreementScore * 100)}%</b>
                </span>
                <span className="council-stat">
                  Confidence <b>{s.confidence}%</b>
                </span>
              </div>
              {s.dissent && (
                <div className="council-synthesis-dissent">
                  <span className="council-dissent-tag">preserved dissent</span>
                  <div className="council-msg-body">{s.dissent}</div>
                </div>
              )}
            </section>
          )}
        </div>

        <div className="diff-modal-foot">
          <span className="diff-foot-spacer" />
          <div className="diff-foot-actions">
            <button className="primary" onClick={onClose}>Close</button>
          </div>
        </div>
      </div>
    </div>,
    document.body,
  );
}
