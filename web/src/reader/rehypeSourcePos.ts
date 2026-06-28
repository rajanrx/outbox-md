import { visit } from "unist-util-visit";

// Inline-level tags: never treated as a comment "block".
const INLINE = new Set([
  "a", "abbr", "b", "bdi", "bdo", "br", "cite", "code", "data", "dfn", "em",
  "i", "kbd", "mark", "q", "rp", "rt", "ruby", "s", "samp", "small", "span",
  "strong", "sub", "sup", "time", "u", "var", "wbr",
]);

// Stamp source character offsets (from mdast/hast position) onto block elements.
export function rehypeSourcePos() {
  return (tree: any) => {
    visit(tree, "element", (node: any) => {
      if (INLINE.has(node.tagName)) return;
      const pos = node.position;
      if (pos?.start?.offset != null && pos?.end?.offset != null) {
        node.properties = node.properties || {};
        node.properties["dataPosStart"] = String(pos.start.offset);
        node.properties["dataPosEnd"] = String(pos.end.offset);
      }
    });
  };
}
