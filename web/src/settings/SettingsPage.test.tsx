// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, waitFor, within } from "@testing-library/react";
import { SettingsPage } from "./SettingsPage";
import type { Project } from "../api";

// Keep isSingleFolderMode + ApiError real; stub only the network functions so the
// page mounts without a server and we can assert which endpoint a save hit.
const baseSettings = {
  auto_update: false,
  auto_reply: true,
  agent_cmd: "claude -p {prompt}",
  council_rounds: 2,
  council_budget: 200000,
  council_deadlock_threshold: 50,
};

vi.mock("../api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api")>();
  return {
    ...actual,
    getSettings: vi.fn(async () => ({ ...baseSettings })),
    putSettings: vi.fn(async (v: Record<string, unknown>) => ({ ...baseSettings, ...v })),
    createProject: vi.fn(async () => ({ name: "new", root: "/new", docs: ["."] })),
    updateProject: vi.fn(async () => ({ name: "repo", root: "/repo", docs: ["docs"] })),
    deleteProject: vi.fn(async () => undefined),
  };
});

import * as api from "../api";

const noop = () => {};
const multi: Project[] = [
  { name: "repo", root: "/repo", docs: ["docs"], members: [], chair: null },
];

afterEach(cleanup);
beforeEach(() => vi.clearAllMocks());

describe("SettingsPage", () => {
  it("renders the three sections and the version", async () => {
    const { getByText, container } = render(
      <SettingsPage projects={multi} initialProject="repo" version="v0.16.0" onBack={noop} onProjectsChanged={noop} />,
    );
    expect(getByText("Projects")).toBeTruthy();
    expect(getByText("Council")).toBeTruthy();
    expect(getByText("Automation")).toBeTruthy();
    expect(getByText("v0.16.0")).toBeTruthy();
    // Council loads its values asynchronously.
    await waitFor(() => expect(container.querySelector('input[type="number"]')).not.toBeNull());
  });

  it("adds and removes a council member in local state", async () => {
    const { container } = render(
      <SettingsPage projects={multi} initialProject="repo" version="dev" onBack={noop} onProjectsChanged={noop} />,
    );
    const card = container.querySelector(".settings-card") as HTMLElement;
    expect(within(card).queryByLabelText("Agent preset")).toBeNull();

    fireEvent.click(within(card).getByText("+ Add member"));
    expect(within(card).getByLabelText("Agent preset")).toBeTruthy();

    fireEvent.click(within(card).getByLabelText("Remove member"));
    await waitFor(() => expect(within(card).queryByLabelText("Agent preset")).toBeNull());
  });

  it("saves a project edit via updateProject", async () => {
    const { container } = render(
      <SettingsPage projects={multi} initialProject="repo" version="dev" onBack={noop} onProjectsChanged={noop} />,
    );
    const card = container.querySelector(".settings-card") as HTMLElement;
    fireEvent.change(within(card).getByRole("textbox"), { target: { value: "docs/specs" } });
    fireEvent.click(within(card).getByText("Save"));
    await waitFor(() => expect(api.updateProject).toHaveBeenCalledTimes(1));
    expect((api.updateProject as ReturnType<typeof vi.fn>).mock.calls[0][0]).toBe("repo");
  });

  it("saves the council guardrails via putSettings", async () => {
    const { getByText, container } = render(
      <SettingsPage projects={multi} initialProject="repo" version="dev" onBack={noop} onProjectsChanged={noop} />,
    );
    await waitFor(() => expect(container.querySelector('input[type="number"]')).not.toBeNull());
    // The Council section's own Save button (scoped to its <section>).
    const council = getByText("Council").closest("section") as HTMLElement;
    fireEvent.click(within(council).getByText("Save"));
    await waitFor(() => expect(api.putSettings).toHaveBeenCalled());
    const patch = (api.putSettings as ReturnType<typeof vi.fn>).mock.calls[0][0];
    expect(Object.keys(patch).sort()).toEqual(["council_budget", "council_deadlock_threshold", "council_rounds"]);
  });

  it("shows the single-folder notice and no projects form in single-folder mode", () => {
    const { getByText, queryByText } = render(
      <SettingsPage projects={[{ name: "", root: "/x", docs: ["."] }]} initialProject="" version="dev" onBack={noop} onProjectsChanged={noop} />,
    );
    expect(getByText(/managed via the CLI/i)).toBeTruthy();
    expect(queryByText("+ Add project")).toBeNull();
  });

  it("blocks save with a chair-required error for 2+ members", async () => {
    const twoMembers: Project[] = [
      { name: "repo", root: "/repo", docs: ["docs"], members: [{ agent: "claude" }, { agent: "codex" }], chair: null },
    ];
    const { container, getByText } = render(
      <SettingsPage projects={twoMembers} initialProject="repo" version="dev" onBack={noop} onProjectsChanged={noop} />,
    );
    const card = container.querySelector(".settings-card") as HTMLElement;
    fireEvent.click(within(card).getByText("Save"));
    expect(getByText(/chair is required/i)).toBeTruthy();
    expect(api.updateProject).not.toHaveBeenCalled();
  });
});
