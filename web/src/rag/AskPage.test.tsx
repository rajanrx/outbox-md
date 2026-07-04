// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, waitFor } from "@testing-library/react";
import { AskPage } from "./AskPage";
import type { Project, RagMessage, RagSession } from "../api";

// A tiny in-memory backend: sessions + messages, driven through the mocked api.
let sessions: RagSession[];
let messages: Record<string, RagMessage[]>;

vi.mock("../api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api")>();
  return {
    ...actual,
    listSessions: vi.fn(async (project = "") => sessions.filter((s) => s.project === project)),
    createSession: vi.fn(async (project = "", title = "") => {
      const s: RagSession = { id: `s${sessions.length + 1}`, project, title: title || "New chat", createdAt: "now" };
      sessions = [s, ...sessions];
      messages[s.id] = [];
      return s;
    }),
    renameSession: vi.fn(async () => undefined),
    deleteSession: vi.fn(async (id: string) => { sessions = sessions.filter((s) => s.id !== id); }),
    listMessages: vi.fn(async (id: string) => messages[id] ?? []),
    askKB: vi.fn(async (id: string, question: string) => {
      const user: RagMessage = { id: `${id}-u`, sessionId: id, role: "user", content: question, sources: [], createdAt: "now" };
      const assistant: RagMessage = {
        id: `${id}-a`, sessionId: id, role: "assistant", content: "Grounded answer text",
        sources: [{ DocPath: "docs/a.md", Heading: "A", Snippet: "x", Score: 1, Source: "hybrid", Kind: "doc" }],
        createdAt: "now",
      };
      messages[id] = [...(messages[id] ?? []), user, assistant];
      return assistant;
    }),
  };
});

import * as api from "../api";

const noop = () => {};
const projects: Project[] = [{ name: "proj-a", root: "/a", docs: ["."], members: [], chair: null }];

beforeEach(() => {
  sessions = [];
  messages = {};
  vi.clearAllMocks();
});
afterEach(cleanup);

describe("AskPage", () => {
  it("creates a session with + New chat", async () => {
    const { getByText } = render(
      <AskPage projects={projects} initialProject="proj-a" version="dev" retrievalMode="semantic" onBack={noop} onOpenDoc={noop} />,
    );
    fireEvent.click(getByText("+ New chat"));
    await waitFor(() => expect(api.createSession).toHaveBeenCalled());
  });

  it("sends a question and renders the answer with its sources", async () => {
    // Seed one session so the input targets it.
    sessions = [{ id: "s1", project: "proj-a", title: "chat", createdAt: "now" }];
    messages = { s1: [] };
    const { getByLabelText, getByText, findByText } = render(
      <AskPage projects={projects} initialProject="proj-a" version="dev" retrievalMode="semantic" onBack={noop} onOpenDoc={noop} />,
    );

    // Wait for the seeded session to load (so send targets it, not a new one).
    await findByText("chat");
    const box = getByLabelText("Ask a question") as HTMLTextAreaElement;
    fireEvent.change(box, { target: { value: "what is A?" } });
    fireEvent.click(getByText("Send"));

    // The ask endpoint is called and the grounded answer + source appear.
    await waitFor(() => expect(api.askKB).toHaveBeenCalledWith("s1", "what is A?"));
    expect(await findByText("Grounded answer text")).toBeTruthy();
    expect(await findByText("docs/a.md")).toBeTruthy();
  });

  it("navigates to a doc when a source is clicked", async () => {
    sessions = [{ id: "s1", project: "proj-a", title: "chat", createdAt: "now" }];
    messages = {
      s1: [
        { id: "m1", sessionId: "s1", role: "user", content: "q", sources: [], createdAt: "now" },
        {
          id: "m2", sessionId: "s1", role: "assistant", content: "ans",
          sources: [{ DocPath: "docs/a.md", Heading: "A", Snippet: "x", Score: 1, Source: "hybrid", Kind: "doc" }],
          createdAt: "now",
        },
      ],
    };
    const onOpenDoc = vi.fn();
    const { findByText } = render(
      <AskPage projects={projects} initialProject="proj-a" version="dev" retrievalMode="semantic" onBack={noop} onOpenDoc={onOpenDoc} />,
    );
    const src = await findByText("docs/a.md");
    fireEvent.click(src);
    expect(onOpenDoc).toHaveBeenCalledWith("docs/a.md");
  });
});
