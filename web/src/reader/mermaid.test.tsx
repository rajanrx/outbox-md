// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MermaidBlock } from "./mermaid";

// jsdom has no canvas, so the real mermaid.render rejects. Stub it to resolve a
// deterministic SVG so we can exercise the toolbar (show-code + fullscreen).
vi.mock("mermaid", () => ({
  default: {
    initialize: vi.fn(),
    render: vi.fn(async (id: string) => ({ svg: `<svg id="${id}"><g></g></svg>` })),
  },
}));

afterEach(cleanup);

const CHART = "graph TD; A-->B;";

describe("MermaidBlock", () => {
  it("toggles between the rendered diagram and the mermaid source", async () => {
    const { container } = render(<MermaidBlock chart={CHART} />);
    await waitFor(() => expect(container.querySelector(".mermaid-render svg")).not.toBeNull());
    expect(container.querySelector(".mermaid-source")).toBeNull();

    // </> Show source — diagram is replaced by selectable <pre><code> of the source.
    fireEvent.click(screen.getByLabelText("Show mermaid source"));
    expect(container.querySelector(".mermaid-source code")?.textContent).toBe(CHART);
    expect(container.querySelector(".mermaid-render")).toBeNull();

    // Toggle back — diagram returns, source gone.
    fireEvent.click(screen.getByLabelText("Show diagram"));
    expect(container.querySelector(".mermaid-source")).toBeNull();
    expect(container.querySelector(".mermaid-render svg")).not.toBeNull();
  });

  it("opens the fullscreen overlay and closes it via Esc and backdrop", async () => {
    render(<MermaidBlock chart={CHART} />);
    const fsBtn = screen.getByLabelText("Fullscreen diagram") as HTMLButtonElement;
    await waitFor(() => expect(fsBtn.disabled).toBe(false)); // enabled once svg resolves

    // Open.
    fireEvent.click(fsBtn);
    expect(document.querySelector(".mermaid-fs-backdrop")).not.toBeNull();
    expect(document.querySelector(".mermaid-fs-body svg")).not.toBeNull();

    // Esc closes the overlay.
    fireEvent.keyDown(document, { key: "Escape" });
    expect(document.querySelector(".mermaid-fs-backdrop")).toBeNull();

    // Reopen, then backdrop click closes it.
    fireEvent.click(fsBtn);
    expect(document.querySelector(".mermaid-fs-backdrop")).not.toBeNull();
    fireEvent.mouseDown(document.querySelector(".mermaid-fs-backdrop")!);
    expect(document.querySelector(".mermaid-fs-backdrop")).toBeNull();
  });

  it("shows the error fallback and disables fullscreen when render fails", async () => {
    const mermaid = (await import("mermaid")).default as unknown as { render: ReturnType<typeof vi.fn> };
    mermaid.render.mockRejectedValueOnce(new Error("bad graph"));
    const { container } = render(<MermaidBlock chart={"graph oops"} />);
    await waitFor(() => expect(container.querySelector(".mermaid-error")).not.toBeNull());
    expect((screen.getByLabelText("Fullscreen diagram") as HTMLButtonElement).disabled).toBe(true);
  });
});
