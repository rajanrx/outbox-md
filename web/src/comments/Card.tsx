import { useEffect, useMemo, useRef, useState } from "react";
import { getThread, reply, resolve, getSuggestion, getCandidates, pickCandidate, type Comment, type ThreadMessage, type Suggestion, type CouncilView } from "../api";
import { unifiedDiff, type Row } from "../suggestion/diff";
import { DiffRows, counts } from "../suggestion/DiffRows";
import { DiffModal } from "../suggestion/DiffModal";
import { CouncilModal } from "./CouncilModal";
import { distinctMembers, finalPositions, isDeadlock, isDiscussing, maxRound, severity } from "./council";

// excerptRows keeps the first ~maxChanges changed (ins/del) lines of a unified
// diff, along with any context/gap rows encountered up to that point, so the
// inline preview stays compact. `truncated` is true when changes were dropped.
function excerptRows(rows: Row[], maxChanges = 6): { rows: Row[]; truncated: boolean } {
  const out: Row[] = [];
  let seen = 0;
  let i = 0;
  for (; i < rows.length; i++) {
    const r = rows[i];
    if (r.op === "ins" || r.op === "del") {
      if (seen >= maxChanges) break;
      seen++;
    }
    out.push(r);
  }
  // Trim a trailing context/gap run so the excerpt ends on a change line.
  while (out.length && out[out.length - 1].op !== "ins" && out[out.length - 1].op !== "del") out.pop();
  const truncated = rows.slice(i).some((r) => r.op === "ins" || r.op === "del");
  return { rows: out, truncated };
}

const LocateIcon = () => (
  <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.4">
    <circle cx="8" cy="8" r="3.2" />
    <path d="M8 1.4v2M8 12.6v2M1.4 8h2M12.6 8h2" strokeLinecap="round" />
  </svg>
);
const PinIcon = ({ filled }: { filled: boolean }) => (
  <svg viewBox="0 0 16 16" fill={filled ? "currentColor" : "none"} stroke="currentColor" strokeWidth="1.3">
    <path d="M6 1.8h4l-.6 3.2 2 2.3v1.3H4.6V7.3l2-2.3L6 1.8z" strokeLinejoin="round" />
    <path d="M8 10.9V14" strokeLinecap="round" />
  </svg>
);

const initial = (who: string) => (who?.[0] || "?").toUpperCase();

export function Card({ comment, currentContent, docPath = "", active = false, pinned = false, offscreen = false, reloadKey = 0, deadlockThreshold = 50, onActivate, onJump, onTogglePin, onChange }: {
  comment: Comment;
  currentContent: string;
  docPath?: string;
  active?: boolean;
  pinned?: boolean;
  offscreen?: boolean;
  // Confidence (0..100) below which a chair verdict is "no consensus" (deadlock),
  // from /api/config. Also the chip's amber/green boundary.
  deadlockThreshold?: number;
  // Bumped by the parent on each SSE event for the open doc; the thread state is
  // local to this Card and would otherwise only load on comment.id change, so an
  // agent reply/suggestion wouldn't appear in the open thread until a refresh.
  reloadKey?: number;
  onActivate?: () => void;
  onJump?: () => void;
  onTogglePin?: () => void;
  onChange: () => void;
}) {
  const [thread, setThread] = useState<ThreadMessage[]>([]);
  const [draft, setDraft] = useState("");
  const ref = useRef<HTMLDivElement>(null);
  // "AI processing…" is a self-expiring hint: show it only while processingUntil
  // is in the future, and hide it on its own at the deadline (a setTimeout) so the
  // badge clears even if no event arrives. A heartbeat that extends the deadline
  // updates processingUntil, re-running this effect and resetting the timer.
  const [processing, setProcessing] = useState(false);
  useEffect(() => {
    const until = comment.processingUntil ? Date.parse(comment.processingUntil) : NaN;
    const ms = until - Date.now();
    if (!Number.isFinite(until) || ms <= 0) { setProcessing(false); return; }
    setProcessing(true);
    const t = setTimeout(() => setProcessing(false), ms);
    return () => clearTimeout(t);
  }, [comment.processingUntil]);
  const load = () => getThread(comment.id).then((t) => setThread(t ?? []));
  useEffect(() => { load(); /* eslint-disable-next-line */ }, [comment.id, reloadKey]);
  useEffect(() => {
    if (active) ref.current?.scrollIntoView({ block: "nearest", behavior: "smooth" });
  }, [active]);

  const stop = (e: React.MouseEvent, fn?: () => void) => { e.stopPropagation(); fn?.(); };
  const sendReply = async () => {
    if (!draft.trim()) return;
    try {
      await reply(comment.id, draft);
      setDraft("");
      await load();
    } catch {
      // reply() throws on a non-OK POST; keep the draft so the comment isn't lost.
    }
  };

  // A comment carries a suggestion in one of two viewable shapes:
  //  • INTERACTIVE — the latest suggestion is still 'proposed' (excerpt + See diff
  //    + Approve/Reject). This can happen while the comment status is
  //    open/claimed/addressed/replied (e.g. an agent that both proposed and
  //    replied leaves it 'replied'); it is diffed against the CURRENT content.
  //  • HISTORICAL — the suggestion was 'accepted' or 'rejected' (read-only:
  //    excerpt + See diff + a status label, no buttons). It is diffed
  //    against-version → proposed, since post-accept the current content already
  //    equals the proposed content. Fetch even for a resolved comment (accept
  //    resolves the comment) so an accepted change stays visible instead of
  //    vanishing. Only a DETACHED comment — a stale/orphaned anchor — is skipped.
  const [sg, setSg] = useState<Suggestion | null>(null);
  const [diffOpen, setDiffOpen] = useState(false);
  // Bumped when the modal completes an Accept/Reject. Those service paths fire no
  // SSE event, so reloadKey (driven by SSE) won't refresh the suggestion; without
  // this the stale 'proposed' sg would linger — the accepted diff would vanish and
  // a rejected one would keep its Approve/Reject buttons. actionTick re-fetches
  // regardless of whether the comment's status changed (reject stays 'open').
  const [actionTick, setActionTick] = useState(0);
  const detached = comment.status === "detached";
  useEffect(() => {
    if (detached) { setSg(null); return; }
    let live = true;
    getSuggestion(comment.id).then((s) => { if (live) setSg(s); });
    return () => { live = false; };
  }, [detached, comment.id, reloadKey, actionTick]);

  // Council read model: null for a plain (non-council) comment — the endpoint
  // 404s and getCandidates maps that to null, so no council UI renders. Fetched
  // alongside the suggestion and refreshed on SSE (reloadKey) + local action
  // (actionTick, bumped after a pick).
  const [council, setCouncil] = useState<CouncilView | null>(null);
  const [councilOpen, setCouncilOpen] = useState(false);
  const [pickBusy, setPickBusy] = useState<string | null>(null);
  const [pickError, setPickError] = useState<string | null>(null);
  useEffect(() => {
    if (detached) { setCouncil(null); return; }
    let live = true;
    getCandidates(comment.id).then((v) => { if (live) setCouncil(v); });
    return () => { live = false; };
  }, [detached, comment.id, reloadKey, actionTick]);

  // Deadlock = the chair reached no consensus (confidence < threshold) AND the
  // human hasn't picked yet → show ranked "no consensus" pick cards instead of the
  // ordinary suggestion. Above threshold (or after a pick) the normal suggestion
  // path is unchanged. deadlock is always false when council is null.
  const deadlock = !!council && isDeadlock(council, deadlockThreshold);
  const pick = async (candidateId: string) => {
    setPickBusy(candidateId);
    setPickError(null);
    try {
      await pickCandidate(comment.id, candidateId);
      setActionTick((t) => t + 1); // refetch council + suggestion
      onChange();                  // refresh the doc/comment (the pick emitted a Suggestion)
    } catch (e) {
      // Keep the cards on screen and surface the server's message verbatim.
      setPickError(e instanceof Error ? e.message : "Pick failed — please try again.");
    } finally {
      setPickBusy(null);
    }
  };
  // Interactive only while proposed AND the comment is not resolved (a stale
  // proposed row on a resolved comment must not offer a Reject that would reopen
  // it). Historical whenever the suggestion is accepted/rejected.
  const interactive = !detached && !!sg && sg.state === "proposed" && comment.status !== "resolved";
  const historical = !detached && !!sg && (sg.state === "accepted" || sg.state === "rejected");
  const showSuggestion = interactive || historical;
  // Interactive diffs against the current content; a historical diff renders what
  // the suggestion changed (against-version → proposed).
  const before = interactive ? currentContent : (sg?.againstContent ?? "");
  const rows = useMemo(
    () => (showSuggestion && sg ? unifiedDiff(before, sg.proposedContent) : []),
    [showSuggestion, sg, before],
  );
  const excerpt = useMemo(() => excerptRows(rows), [rows]);
  const changed = rows.some((r) => r.op === "ins" || r.op === "del");
  const c = counts(rows);
  const stateLabel = sg?.state === "accepted" ? "Accepted" : sg?.state === "rejected" ? "Rejected" : "";

  return (
    <div ref={ref} className={"card" + (active ? " active" : "") + (pinned ? " pinned" : "")} data-comment={comment.id} onClick={onActivate}>
      <div className="card-bar">
        <span className={`status-tag status-${comment.status}`}>{comment.status}</span>
        {comment.postApproval && (
          <span className="post-approval-tag" title="Feedback added after approval">post-approval</span>
        )}
        <span className="card-tools">
          {offscreen && (
            <button className="ic-btn" title="Scroll to text" aria-label="Scroll to text" onClick={(e) => stop(e, onJump)}><LocateIcon /></button>
          )}
          <button className={"ic-btn" + (pinned ? " on" : "")} title={pinned ? "Unpin" : "Pin"} aria-label={pinned ? "Unpin" : "Pin"} onClick={(e) => stop(e, onTogglePin)}><PinIcon filled={pinned} /></button>
        </span>
      </div>

      <div className="posts">
        {thread.length === 0 && <div className="post-empty">No comment text yet.</div>}
        {thread.map((m) => (
          <div key={m.id} className="post">
            <span className={`avatar who-${m.authorIdentity}`}>{initial(m.authorIdentity)}</span>
            <div className="post-main">
              <div className="post-author">{m.authorIdentity}</div>
              <div className="post-text">{m.body}</div>
            </div>
          </div>
        ))}
        {/* Pending-reply hint at the BOTTOM of the thread (under the latest human
            message being processed), so it's visible on long threads — not lost
            in the card header. */}
        {processing && (
          <div className="post processing-post" aria-live="polite">
            <span className="avatar who-agent">⋯</span>
            <div className="post-main">
              <div className="processing-tag" title="An AI agent is working on this comment">⋯ AI processing</div>
            </div>
          </div>
        )}
      </div>

      {council && (
        <div className="council-summary" onClick={(e) => e.stopPropagation()}>
          <div className="council-summary-head">
            {council.synthesis && (
              <span
                className={`council-confidence sev-${severity(council.synthesis.confidence, deadlockThreshold)}`}
                title="Chair confidence in the verdict"
              >
                {council.synthesis.confidence}% confidence
              </span>
            )}
            {council.synthesis && (
              <span className="council-agreement" title="Council agreement">
                {Math.round(council.synthesis.agreementScore * 100)}% agree
              </span>
            )}
            <span className="council-members" title="Council members / rounds">
              {distinctMembers(council)} members{maxRound(council) > 0 ? ` · round ${maxRound(council)}` : ""}
            </span>
            {isDiscussing(council) && <span className="council-discussing" title="Members debated over rounds">discussing</span>}
          </div>
          <button className="council-view-btn" onClick={() => setCouncilOpen(true)}>View discussion →</button>
        </div>
      )}

      {deadlock && (
        <div className="council-deadlock" onClick={(e) => e.stopPropagation()}>
          <div className="council-deadlock-head">No consensus — pick a direction</div>
          {finalPositions(council!).map((cand, i) => (
            <div key={cand.id} className="council-option">
              <div className="council-option-head">
                <span className="council-rank">#{i + 1}</span>
                <span className="council-who">{cand.agentIdentity}</span>
                <span className={`council-verdict verdict-${cand.verdict}`}>{cand.verdict.replace("_", " ")}</span>
              </div>
              {cand.rationale && <div className="council-rationale">{cand.rationale}</div>}
              {cand.verdict === "edit" && cand.content && (
                <pre className="council-option-preview">{cand.content.slice(0, 240)}{cand.content.length > 240 ? "…" : ""}</pre>
              )}
              <button className="council-pick-btn" disabled={pickBusy !== null} onClick={() => pick(cand.id)}>
                {pickBusy === cand.id ? "Picking…" : "Pick this"}
              </button>
            </div>
          ))}
          {pickError && <div className="council-pick-error" role="alert">{pickError}</div>}
        </div>
      )}

      {councilOpen && council && (
        <CouncilModal view={council} title={docPath || "Council discussion"} onClose={() => setCouncilOpen(false)} />
      )}

      {showSuggestion && !deadlock && (
        <div className="suggestion" onClick={(e) => e.stopPropagation()}>
          <div className="suggestion-head">
            Suggested change
            {historical && <span className={`suggestion-state state-${sg!.state}`}>{stateLabel}</span>}
            {changed && <span className="diff-count"><span className="ins">+{c.ins}</span> <span className="del">−{c.del}</span></span>}
          </div>
          {changed ? (
            <>
              <DiffRows rows={excerpt.rows} />
              {excerpt.truncated && <div className="diff-more">… more changes</div>}
            </>
          ) : (
            <div className="diff-empty">No textual changes from the current version.</div>
          )}
          <div className="diff-actions">
            <button className="primary" onClick={(e) => stop(e, () => setDiffOpen(true))}>See diff →</button>
          </div>
        </div>
      )}

      {showSuggestion && diffOpen && (
        <DiffModal
          open={diffOpen}
          commentId={comment.id}
          currentContent={currentContent}
          title={docPath || "Suggested change"}
          onClose={() => setDiffOpen(false)}
          onChange={() => { setActionTick((t) => t + 1); onChange(); }}
        />
      )}

      {comment.status !== "resolved" && (
        <div className="card-actions" onClick={(e) => e.stopPropagation()}>
          <input
            value={draft} placeholder="Reply…"
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => { if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); sendReply(); } }}
          />
          <button className="link-btn" disabled={!draft.trim()} onClick={sendReply}>Reply</button>
          <button className="link-btn resolve" onClick={async () => { await resolve(comment.id); onChange(); }}>Resolve</button>
        </div>
      )}
    </div>
  );
}
