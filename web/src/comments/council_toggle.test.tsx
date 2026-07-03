// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, waitFor } from "@testing-library/react";

// Mock the whole api surface the Card touches. setCommentCouncil is the one under
// test; the rest are inert stubs.
const api = vi.hoisted(() => ({
  getThread: vi.fn(async () => []),
  reply: vi.fn(async () => null),
  resolve: vi.fn(async () => null),
  getSuggestion: vi.fn(async () => null),
  getCandidates: vi.fn(async () => null),
  pickCandidate: vi.fn(async () => {}),
  setCommentCouncil: vi.fn(async () => {}),
}));
vi.mock("../api", () => api);

import { Card } from "./Card";

const comment = { id: "c1", anchor: { start: 0, end: 4 }, status: "open", authorIdentity: "human", postApproval: false };

beforeEach(() => {
  Object.values(api).forEach((f) => f.mockClear());
  api.getCandidates.mockResolvedValue(null);
  api.getSuggestion.mockResolvedValue(null);
  api.setCommentCouncil.mockResolvedValue(undefined);
  localStorage.clear();
});
afterEach(cleanup);

describe("per-comment council toggle", () => {
  it("renders the toggle on a council project reflecting councilEnabled=false", () => {
    const { container, getByRole } = render(
      <Card comment={{ ...comment, councilEnabled: false } as never} currentContent="hello" councilProject onChange={() => {}} />,
    );
    expect(container.querySelector(".council-toggle")).not.toBeNull();
    expect((getByRole("radio", { name: "Single agent" }) as HTMLInputElement).checked).toBe(true);
    expect((getByRole("radio", { name: "On" }) as HTMLInputElement).checked).toBe(false);
  });

  it("reflects councilEnabled=true", () => {
    const { getByRole } = render(
      <Card comment={{ ...comment, councilEnabled: true } as never} currentContent="hello" councilProject onChange={() => {}} />,
    );
    expect((getByRole("radio", { name: "On" }) as HTMLInputElement).checked).toBe(true);
  });

  it("toggling On calls the endpoint and persists the choice to localStorage", async () => {
    const onChange = vi.fn();
    const { getByRole } = render(
      <Card comment={{ ...comment, councilEnabled: false } as never} currentContent="hello" councilProject onChange={onChange} />,
    );
    fireEvent.click(getByRole("radio", { name: "On" }));
    await waitFor(() => expect(api.setCommentCouncil).toHaveBeenCalledWith("c1", true));
    expect(localStorage.getItem("outbox.councilEnabled")).toBe("true");
    await waitFor(() => expect(onChange).toHaveBeenCalled());
  });

  it("seeds a NEW comment's toggle from the remembered choice when the comment has no flag", () => {
    localStorage.setItem("outbox.councilEnabled", "true");
    const { getByRole } = render(
      // councilEnabled omitted (a fresh comment before the server flag is read).
      <Card comment={comment as never} currentContent="hello" councilProject councilDefault={false} onChange={() => {}} />,
    );
    expect((getByRole("radio", { name: "On" }) as HTMLInputElement).checked).toBe(true);
  });

  it("hides the toggle on a non-council project", () => {
    const { container } = render(
      <Card comment={{ ...comment, councilEnabled: true } as never} currentContent="hello" onChange={() => {}} />,
    );
    expect(container.querySelector(".council-toggle")).toBeNull();
  });
});
