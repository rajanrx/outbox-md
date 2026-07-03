// @vitest-environment jsdom
import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { RetrievalChip } from "./RetrievalChip";

afterEach(cleanup);

describe("RetrievalChip", () => {
  it("renders an info chip for keyword-only", () => {
    render(<RetrievalChip mode="keyword-only" />);
    const chip = screen.getByRole("status");
    expect(chip.className).toContain("info");
    expect(chip.className).not.toContain("warn");
    expect(chip.textContent).toMatch(/keyword search/i);
    expect(chip.getAttribute("title") ?? "").toMatch(/install ollama/i);
  });

  it("renders a warning chip for degraded", () => {
    render(<RetrievalChip mode="degraded" />);
    const chip = screen.getByRole("status");
    expect(chip.className).toContain("warn");
    expect(chip.className).not.toContain("info");
    expect(chip.getAttribute("title") ?? "").toMatch(/embedder unavailable/i);
  });

  it("renders nothing for semantic", () => {
    const { container } = render(<RetrievalChip mode="semantic" />);
    expect(container.firstChild).toBeNull();
    expect(screen.queryByRole("status")).toBeNull();
  });

  it("renders nothing when the mode is empty/absent (retrieval unwired)", () => {
    const { container: a } = render(<RetrievalChip mode="" />);
    expect(a.firstChild).toBeNull();
    const { container: b } = render(<RetrievalChip mode={undefined} />);
    expect(b.firstChild).toBeNull();
  });
});
