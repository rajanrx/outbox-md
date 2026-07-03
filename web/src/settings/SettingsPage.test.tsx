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
  council_default: false,
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
    expect(Object.keys(patch).sort()).toEqual(["council_budget", "council_deadlock_threshold", "council_default", "council_rounds"]);
  });

  it("saves the council_default toggle via putSettings", async () => {
    const { getByText, container } = render(
      <SettingsPage projects={multi} initialProject="repo" version="dev" onBack={noop} onProjectsChanged={noop} />,
    );
    await waitFor(() => expect(container.querySelector('input[type="number"]')).not.toBeNull());
    const council = getByText("Council").closest("section") as HTMLElement;
    // Flip "Council on new comments by default" (the section's checkbox), then Save.
    fireEvent.click(within(council).getByRole("checkbox"));
    fireEvent.click(within(council).getByText("Save"));
    await waitFor(() => expect(api.putSettings).toHaveBeenCalled());
    const patch = (api.putSettings as ReturnType<typeof vi.fn>).mock.calls[0][0];
    expect(patch.council_default).toBe(true);
  });

  it("shows the single-folder notice and no projects form in single-folder mode", () => {
    const { getByText, queryByText } = render(
      <SettingsPage projects={[{ name: "", root: "/x", docs: ["."] }]} initialProject="" version="dev" onBack={noop} onProjectsChanged={noop} />,
    );
    expect(getByText(/managed via the CLI/i)).toBeTruthy();
    expect(queryByText("+ Add project")).toBeNull();
  });

  it("falls back to the CLI notice when a project write returns 409", async () => {
    (api.updateProject as ReturnType<typeof vi.fn>).mockRejectedValueOnce(
      new api.ApiError(409, "projects API requires registry mode"),
    );
    const { container, getByText, queryByText } = render(
      <SettingsPage projects={multi} initialProject="repo" version="dev" onBack={noop} onProjectsChanged={noop} />,
    );
    const card = container.querySelector(".settings-card") as HTMLElement;
    fireEvent.click(within(card).getByText("Save"));
    await waitFor(() => expect(getByText(/managed via the CLI/i)).toBeTruthy());
    expect(queryByText("+ Add project")).toBeNull();
  });

  it("preserves an unsaved Council edit when saving the Automation section (P2)", async () => {
    const { getByText, container } = render(
      <SettingsPage projects={multi} initialProject="repo" version="dev" onBack={noop} onProjectsChanged={noop} />,
    );
    await waitFor(() => expect(container.querySelector('input[type="number"]')).not.toBeNull());
    // Edit council_rounds (first number input in the Council section) but do NOT save it.
    const council = getByText("Council").closest("section") as HTMLElement;
    const rounds = within(council).getAllByRole("spinbutton")[0] as HTMLInputElement;
    fireEvent.change(rounds, { target: { value: "7" } });
    expect(rounds.value).toBe("7");
    // Save the OTHER section. Its putSettings response carries the OLD council_rounds;
    // the unsaved edit must survive (only the saved keys merge back).
    const automation = getByText("Automation").closest("section") as HTMLElement;
    fireEvent.click(within(automation).getByText("Save"));
    await waitFor(() => expect(api.putSettings).toHaveBeenCalled());
    await waitFor(() =>
      expect((within(council).getAllByRole("spinbutton")[0] as HTMLInputElement).value).toBe("7"),
    );
  });

  it("resyncs the selected project when the current one is removed (P2)", async () => {
    const two: Project[] = [
      { name: "repoA", root: "/a", docs: ["."], members: [], chair: null },
      { name: "repoB", root: "/b", docs: ["."], members: [], chair: null },
    ];
    const { rerender } = render(
      <SettingsPage projects={two} initialProject="repoA" version="dev" onBack={noop} onProjectsChanged={noop} />,
    );
    await waitFor(() => expect(api.getSettings).toHaveBeenCalledWith("repoA"));
    (api.getSettings as ReturnType<typeof vi.fn>).mockClear();
    // repoA removed via CRUD → only repoB remains. proj must resync to a valid
    // project instead of keeping /api/settings?project=repoA (the deleted one).
    rerender(
      <SettingsPage projects={[two[1]]} initialProject="repoA" version="dev" onBack={noop} onProjectsChanged={noop} />,
    );
    await waitFor(() => expect(api.getSettings).toHaveBeenCalledWith("repoB"));
  });

  it("blocks save when the chair is cleared on a 2+ member project", async () => {
    // The chair defaults to a preset (claude), so a fresh 2-member project saves
    // fine; the required-chair block fires only when the user CLEARS the chair
    // (switching its agent to a custom command and leaving it blank).
    const twoMembers: Project[] = [
      { name: "repo", root: "/repo", docs: ["docs"], members: [{ agent: "claude" }, { agent: "codex" }], chair: { agent: "claude" } },
    ];
    const { container, getByText } = render(
      <SettingsPage projects={twoMembers} initialProject="repo" version="dev" onBack={noop} onProjectsChanged={noop} />,
    );
    const card = container.querySelector(".settings-card") as HTMLElement;
    const chairSection = card.querySelector(".settings-chair") as HTMLElement;
    const chairSelect = within(chairSection).getByLabelText("Agent preset") as HTMLSelectElement;
    fireEvent.change(chairSelect, { target: { value: "custom" } }); // blanks the chair agent
    fireEvent.click(within(card).getByText("Save"));
    expect(getByText(/chair is required/i)).toBeTruthy();
    expect(api.updateProject).not.toHaveBeenCalled();
  });

  it("shows the per-agent model datalist so a supported model can be picked", async () => {
    const { container } = render(
      <SettingsPage projects={multi} initialProject="repo" version="dev" onBack={noop} onProjectsChanged={noop} />,
    );
    const card = container.querySelector(".settings-card") as HTMLElement;
    fireEvent.click(within(card).getByText("+ Add member")); // defaults to claude
    const modelInput = within(card).getAllByLabelText("Model")[0] as HTMLInputElement;
    expect(modelInput.getAttribute("list")).toBe("models-claude");
    // The claude datalist carries real supported models (an alias + a full id).
    const list = card.querySelector("#models-claude") as HTMLElement;
    const opts = Array.from(list.querySelectorAll("option")).map((o) => o.getAttribute("value"));
    expect(opts).toContain("opus");
    expect(opts).toContain("claude-opus-4-8");
  });
});
