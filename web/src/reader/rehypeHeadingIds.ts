import { visit } from "unist-util-visit";

const HEADINGS = new Set(["h1", "h2", "h3", "h4", "h5", "h6"]);

// GitHub-compatible slug: lowercase, drop punctuation (keep word chars, hyphen,
// space), then turn each space into a hyphen. Matches the anchors used by
// Markdown tables-of-contents generated against GitHub's algorithm.
function ghSlug(text: string): string {
  return text
    .trim()
    .toLowerCase()
    .replace(/[^\w\- ]+/g, "")
    .replace(/ /g, "-");
}

function textOf(node: any): string {
  if (node.type === "text") return node.value ?? "";
  if (node.children) return node.children.map(textOf).join("");
  return "";
}

// Stamp GitHub-style ids onto headings so in-page anchor links resolve.
export function rehypeHeadingIds() {
  return (tree: any) => {
    const seen = new Map<string, number>();
    visit(tree, "element", (node: any) => {
      if (!HEADINGS.has(node.tagName)) return;
      node.properties = node.properties || {};
      if (node.properties.id) return;
      const base = ghSlug(textOf(node)) || "section";
      const n = seen.get(base) ?? 0;
      seen.set(base, n + 1);
      node.properties.id = n === 0 ? base : `${base}-${n}`;
    });
  };
}
