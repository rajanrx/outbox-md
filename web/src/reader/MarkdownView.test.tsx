// @vitest-environment jsdom
import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render } from "@testing-library/react";
import { MarkdownView } from "./MarkdownView";

afterEach(cleanup);

describe("MarkdownView", () => {
  it("renders a heading, a list and a GFM table", () => {
    const md = [
      "# Title",
      "",
      "- one",
      "- two",
      "",
      "| A | B |",
      "| - | - |",
      "| 1 | 2 |",
      "",
    ].join("\n");
    const { container } = render(<MarkdownView content={md} />);

    const h1 = container.querySelector("h1");
    expect(h1?.textContent).toBe("Title");
    expect(container.querySelectorAll("li")).toHaveLength(2);
    // remark-gfm turns the pipe table into a real <table> with a body row.
    const table = container.querySelector("table");
    expect(table).not.toBeNull();
    expect(table?.querySelectorAll("tbody td")).toHaveLength(2);
  });

  it("stamps heading ids (GitHub slug) for anchor nav", () => {
    const { container } = render(<MarkdownView content={"## Hello World\n"} />);
    expect(container.querySelector("h2")?.id).toBe("hello-world");
  });

  it("routes a ```mermaid fence to MermaidBlock, not a raw <code>", () => {
    const md = ["```mermaid", "graph TD; A-->B;", "```", ""].join("\n");
    const { container } = render(<MarkdownView content={md} />);

    // MermaidBlock renders the [data-mermaid] wrapper. mermaid.render is async +
    // canvas-bound (rejects in jsdom → error fallback), so we assert the block was
    // CHOSEN, never its SVG output.
    expect(container.querySelector("[data-mermaid]")).not.toBeNull();
    expect(container.querySelector("code.language-mermaid")).toBeNull();
  });
});
