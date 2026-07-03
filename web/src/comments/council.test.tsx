// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, waitFor } from "@testing-library/react";
import type { CouncilView } from "../api";

// One mock for the whole api surface Card / DiffModal / CouncilModal touch. The
// per-test bodies below re-point getCandidates / pickCandidate as needed.
// vi.hoisted so the object exists before the hoisted vi.mock factory runs.
const api = vi.hoisted(() => ({
  getThread: vi.fn(async () => []),
  reply: vi.fn(async () => null),
  resolve: vi.fn(async () => null),
  getSuggestion: vi.fn(async () => null),
  getCandidates: vi.fn(async () => null as CouncilView | null),
  pickCandidate: vi.fn(async () => {}),
  getPendingSuggestions: vi.fn(async () => []),
  accept: vi.fn(async () => null),
  rejectSuggestion: vi.fn(async () => null),
}));
vi.mock("../api", () => api);

// Imported AFTER the mock is registered.
import { Card } from "./Card";
import { CouncilModal } from "./CouncilModal";

const comment = { id: "c1", anchor: { start: 0, end: 4 }, status: "open", authorIdentity: "human", postApproval: false };

// A full council view: 3 blind takes (round 0), a round-1 revision + discussion
// message (the debate), and a chair synthesis. Two members land on "edit", the
// skeptic (codex) holds "reject_comment" → codex is the dissenter (edit plurality).
function council(overrides: Partial<CouncilView> = {}): CouncilView {
  return {
    set: { id: "set1", commentId: "c1", state: "gathering", quorum: 3 },
    candidates: [
      { id: "cand-a", candidateSetId: "set1", round: 0, lens: "correctness", verdict: "edit", rationale: "Tighten the wording.", content: "# Tighter heading\n", agentIdentity: "claude", chosen: false },
      { id: "cand-c", candidateSetId: "set1", round: 0, lens: "completeness", verdict: "edit", rationale: "Also cover the edge case.", content: "# Tighter heading\n\nEdge case covered.\n", agentIdentity: "gemini", chosen: false },
      { id: "cand-b", candidateSetId: "set1", round: 0, lens: "skeptic", verdict: "reject_comment", rationale: "The comment misreads the spec.", content: "", agentIdentity: "codex", chosen: false },
      { id: "cand-b1", candidateSetId: "set1", round: 1, lens: "skeptic", verdict: "reject_comment", rationale: "Still a misread after discussion.", content: "", agentIdentity: "codex", chosen: false },
    ],
    discussion: [
      { id: "msg-1", candidateSetId: "set1", round: 1, agentIdentity: "codex", body: "I still disagree after review.", refs: [{ kind: "member", targetId: "cand-a" }, { kind: "kb", targetId: "spec.md#3" }] },
    ],
    synthesis: { id: "syn1", candidateSetId: "set1", agreementScore: 0.5, confidence: 82, dissent: "codex would reject the comment outright.", summary: "Tighten the heading; the change sharpens the claim without losing meaning.", suggestionId: "sg1", createdBy: "chair" },
    ...overrides,
  };
}

beforeEach(() => {
  Object.values(api).forEach((f) => f.mockClear());
  api.getCandidates.mockResolvedValue(null);
  api.getSuggestion.mockResolvedValue(null);
  api.pickCandidate.mockResolvedValue(undefined);
});
afterEach(cleanup);

const renderCard = () =>
  render(
    <Card comment={comment as never} currentContent="hello world" deadlockThreshold={50} onChange={() => {}} />,
  );

describe("council Card summary", () => {
  it("shows the confidence chip + agreement + View discussion on a council comment", async () => {
    api.getCandidates.mockResolvedValue(council());
    const { container } = renderCard();

    await waitFor(() => expect(container.querySelector(".council-summary")).not.toBeNull());
    const chip = container.querySelector(".council-confidence")!;
    expect(chip.textContent).toContain("82% confidence");
    // 82 >= 50 threshold → high severity (quiet/green), not amber.
    expect(chip.className).toContain("sev-high");
    expect(container.querySelector(".council-agreement")?.textContent).toContain("50% agree");
    expect(container.querySelector(".council-members")?.textContent).toContain("3 members");
    // round 1 discussion happened → "discussing" indicator + round hint.
    expect(container.querySelector(".council-discussing")).not.toBeNull();
    expect(container.querySelector(".council-members")?.textContent).toContain("round 1");
    expect(container.querySelector(".council-view-btn")?.textContent).toContain("View discussion");
  });

  it("renders NO council UI for a non-council (404 → null) comment", async () => {
    api.getCandidates.mockResolvedValue(null);
    const { container } = renderCard();
    // Let the async fetches settle.
    await waitFor(() => expect(api.getCandidates).toHaveBeenCalled());
    await Promise.resolve();
    expect(container.querySelector(".council-summary")).toBeNull();
    expect(container.querySelector(".council-deadlock")).toBeNull();
    expect(container.querySelector(".council-confidence")).toBeNull();
  });
});

describe("council discussion modal", () => {
  it("groups by round with attribution, shows an edit candidate's content and the dissent", () => {
    render(<CouncilModal view={council()} title="doc.md" onClose={() => {}} />);

    const rounds = [...document.querySelectorAll(".council-round-title")].map((n) => n.textContent);
    expect(rounds[0]).toContain("Round 0");
    expect(rounds[0]).toContain("Independent takes");
    expect(rounds[1]).toContain("Round 1");
    expect(rounds[1]).toContain("Discussion");

    // Attribution: identities + lens badges present.
    const body = document.body.textContent || "";
    expect(body).toContain("claude");
    expect(body).toContain("codex");
    expect(document.querySelector(".council-lens")?.textContent).toBe("correctness");

    // The edit candidate exposes its proposed content.
    expect(document.querySelector(".council-proposed-body")?.textContent).toContain("Tighter heading");

    // Dissent: codex's reject_comment differs from the plurality → dissent styling,
    // plus the chair's preserved dissent text.
    const dissenting = [...document.querySelectorAll(".council-entry.council-dissent")];
    expect(dissenting.length).toBeGreaterThan(0);
    expect(dissenting.some((n) => (n.textContent || "").includes("codex"))).toBe(true);
    expect(body).toContain("preserved dissent");
    expect(body).toContain("codex would reject the comment outright.");

    // Citations: a member ref resolves to the author, a KB ref shows the source.
    const refs = [...document.querySelectorAll(".council-ref")].map((n) => n.textContent);
    expect(refs.some((t) => t?.includes("responding to claude"))).toBe(true);
    expect(refs.some((t) => t?.includes("KB: spec.md#3"))).toBe(true);

    // Chair synthesis: derived decision label (a suggestion is set → an edit) and
    // the chair's summary prose render above the stats.
    expect(document.querySelector(".council-synthesis-decision")?.textContent).toContain("Edit proposed");
    expect(document.querySelector(".council-synthesis-summary")?.textContent).toContain(
      "Tighten the heading",
    );

    // Chair synthesis stats: agreement scaled to %, confidence as-is.
    const stats = document.querySelector(".council-synthesis-stats")?.textContent || "";
    expect(stats).toContain("50%");
    expect(stats).toContain("82%");
  });

  it("shows a 'Reply — no edit' decision and the summary when no suggestion was emitted", () => {
    render(
      <CouncilModal
        view={council({
          synthesis: {
            id: "syn1",
            candidateSetId: "set1",
            agreementScore: 0.97,
            confidence: 88,
            dissent: "",
            summary: "Outbox turns Markdown comments into AI-reviewed edits.",
            suggestionId: "",
            createdBy: "chair",
          },
        })}
        title="doc.md"
        onClose={() => {}}
      />,
    );
    expect(document.querySelector(".council-synthesis-decision")?.textContent).toContain("Reply — no edit");
    expect(document.querySelector(".council-synthesis-summary")?.textContent).toContain(
      "Outbox turns Markdown comments",
    );
  });
});

describe("deadlock / no-consensus pick cards", () => {
  // Below the threshold + no pick yet → ranked option cards instead of a suggestion.
  const deadlockView = () =>
    council({ synthesis: { id: "syn1", candidateSetId: "set1", agreementScore: 0.3, confidence: 30, dissent: "", suggestionId: "", createdBy: "chair" } });

  it("renders pick cards below the threshold and Pick calls pickCandidate", async () => {
    api.getCandidates.mockResolvedValue(deadlockView());
    const onChange = vi.fn();
    const { container } = render(
      <Card comment={comment as never} currentContent="hello world" deadlockThreshold={50} onChange={onChange} />,
    );

    await waitFor(() => expect(container.querySelector(".council-deadlock")).not.toBeNull());
    // Amber chip (30 < 50).
    expect(container.querySelector(".council-confidence")?.className).toContain("sev-low");
    const options = container.querySelectorAll(".council-option");
    // One option per member's final position (claude, gemini, codex).
    expect(options.length).toBe(3);
    // Ranked #1..
    expect(container.querySelector(".council-rank")?.textContent).toBe("#1");

    fireEvent.click(container.querySelectorAll(".council-pick-btn")[0]);
    await waitFor(() => expect(api.pickCandidate).toHaveBeenCalledWith("c1", "cand-a"));
    await waitFor(() => expect(onChange).toHaveBeenCalled());
  });

  it("surfaces the server message on a failed pick and keeps the cards", async () => {
    api.getCandidates.mockResolvedValue(deadlockView());
    api.pickCandidate.mockRejectedValue(new Error("already decided"));
    const { container } = render(
      <Card comment={comment as never} currentContent="hello world" deadlockThreshold={50} onChange={() => {}} />,
    );

    await waitFor(() => expect(container.querySelector(".council-deadlock")).not.toBeNull());
    fireEvent.click(container.querySelectorAll(".council-pick-btn")[0]);

    await waitFor(() => expect(container.querySelector(".council-pick-error")?.textContent).toContain("already decided"));
    // Cards are still there — the pick wasn't silently swallowed.
    expect(container.querySelectorAll(".council-option").length).toBe(3);
  });
});
