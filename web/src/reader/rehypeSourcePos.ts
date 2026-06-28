import { visit } from "unist-util-visit";

// Stamp source character offsets from mdast/hast position data onto elements.
export function rehypeSourcePos() {
  return (tree: any) => {
    visit(tree, "element", (node: any) => {
      const pos = node.position;
      if (pos?.start?.offset != null && pos?.end?.offset != null) {
        node.properties = node.properties || {};
        node.properties["dataPosStart"] = String(pos.start.offset);
        node.properties["dataPosEnd"] = String(pos.end.offset);
      }
    });
  };
}
