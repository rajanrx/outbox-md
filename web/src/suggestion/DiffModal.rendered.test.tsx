// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, waitFor } from "@testing-library/react";
import { DiffModal } from "./DiffModal";

// Stub the network so the modal loads a known proposed doc synchronously-ish.
vi.mock("../api", () => ({
  getSuggestion: vi.fn(async () => ({
    id: "s1",
    proposedContent: "# Proposed Heading\n\n- alpha\n- beta\n",
    state: "proposed",
  })),
  getPendingSuggestions: vi.fn(async () => []),
  accept: vi.fn(),
  rejectSuggestion: vi.fn(),
  reply: vi.fn(),
}));

afterEach(cleanup);
// Force the persisted view mode to the new third option before each render.
beforeEach(() => localStorage.setItem("outbox-diff-view", "rendered"));

describe("DiffModal — Rendered mode", () => {
  it("renders the proposed content through MarkdownView, not a text diff", async () => {
    render(
      <DiffModal
        open
        commentId="c1"
        currentContent="# Old Heading\n"
        title="doc.md"
        onClose={() => {}}
        onChange={() => {}}
      />,
    );

    // The rendered-view frame appears once the suggestion resolves.
    await waitFor(() => expect(document.querySelector(".rendered-view")).not.toBeNull());

    // Proposed markdown is rendered to real HTML (heading text from proposedContent).
    const h1 = document.querySelector(".rendered-view h1");
    expect(h1?.textContent).toBe("Proposed Heading");

    // Crucially: NO text-diff surface is mounted in Rendered mode.
    expect(document.querySelector(".diff-split")).toBeNull();
    expect(document.querySelector(".diff-inline")).toBeNull();
  });
});
