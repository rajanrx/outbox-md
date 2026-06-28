import "./sidebar.css";

export function Sidebar({ docs, activeId, onSelect }: {
  docs: { id: string; path: string }[];
  activeId: string;
  onSelect: (id: string) => void;
}) {
  return (
    <nav className="sidebar">
      <div className="sidebar-title">Documents</div>
      {docs.map((d) => (
        <button key={d.id} className={d.id === activeId ? "doc active" : "doc"} onClick={() => onSelect(d.id)}>
          {d.path}
        </button>
      ))}
    </nav>
  );
}
