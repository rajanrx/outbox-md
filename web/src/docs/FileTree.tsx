import { useState } from "react";

type Doc = { id: string; path: string };
type Node = { name: string; path: string; id?: string; children: Map<string, Node> };

function buildTree(docs: Doc[]): Node {
  const root: Node = { name: "", path: "", children: new Map() };
  for (const d of docs) {
    const parts = d.path.split("/");
    let cur = root;
    parts.forEach((part, i) => {
      let child = cur.children.get(part);
      if (!child) {
        child = { name: part, path: parts.slice(0, i + 1).join("/"), children: new Map() };
        cur.children.set(part, child);
      }
      if (i === parts.length - 1) child.id = d.id;
      cur = child;
    });
  }
  return root;
}

const ordered = (n: Node) =>
  [...n.children.values()].sort((a, b) => {
    const af = a.id == null, bf = b.id == null;
    if (af !== bf) return af ? -1 : 1; // folders first
    return a.name.localeCompare(b.name);
  });

const Chevron = () => (
  <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.6">
    <path d="M6 4l4 4-4 4" strokeLinecap="round" strokeLinejoin="round" />
  </svg>
);
const FolderIcon = () => (
  <svg className="ic" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.3">
    <path d="M1.5 4.2c0-.6.4-1 1-1h3l1.3 1.4h5.7c.6 0 1 .4 1 1v6c0 .6-.4 1-1 1h-10c-.6 0-1-.4-1-1V4.2z" strokeLinejoin="round" />
  </svg>
);
const FileIcon = () => (
  <svg className="ic" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.3">
    <path d="M4 1.5h5L12.5 5v9.5h-8.5z" strokeLinejoin="round" />
    <path d="M9 1.5V5h3.5" strokeLinejoin="round" />
  </svg>
);

function Row({ node, depth, activeId, onSelect }: {
  node: Node; depth: number; activeId: string; onSelect: (id: string) => void;
}) {
  const [open, setOpen] = useState(true);
  const pad = { paddingLeft: 8 + depth * 14 };
  if (node.id != null) {
    return (
      <button
        className={"tree-row file" + (node.id === activeId ? " active" : "")}
        style={pad}
        onClick={() => onSelect(node.id!)}
        title={node.path}
      >
        <span className="chev" />
        <FileIcon />
        <span className="name">{node.name}</span>
      </button>
    );
  }
  return (
    <>
      <button className="tree-row folder" style={pad} onClick={() => setOpen((o) => !o)}>
        <span className={"chev" + (open ? " open" : "")}><Chevron /></span>
        <FolderIcon />
        <span className="name">{node.name}</span>
      </button>
      {open && (
        <div className="tree-children">
          {ordered(node).map((k) => (
            <Row key={k.path} node={k} depth={depth + 1} activeId={activeId} onSelect={onSelect} />
          ))}
        </div>
      )}
    </>
  );
}

export function FileTree({ docs, activeId, onSelect }: {
  docs: Doc[]; activeId: string; onSelect: (id: string) => void;
}) {
  const root = buildTree(docs);
  return (
    <div className="tree">
      {ordered(root).map((n) => (
        <Row key={n.path} node={n} depth={0} activeId={activeId} onSelect={onSelect} />
      ))}
    </div>
  );
}
