import { useEffect } from "react";
import { MarkdownView } from "./MarkdownView";
import "./reader.css";

export function Reader({ content, rootRef }: {
  content: string;
  rootRef: React.RefObject<HTMLDivElement | null>;
}) {
  // In-page TOC anchors: the scroll container is the reader pane (not window),
  // so resolve the target by id and scroll it into view ourselves.
  const onClick = (e: React.MouseEvent<HTMLDivElement>) => {
    const a = (e.target as HTMLElement).closest("a");
    const href = a?.getAttribute("href");
    if (!href || !href.startsWith("#")) return;
    const id = decodeURIComponent(href.slice(1));
    const target = document.getElementById(id);
    if (!target) return;
    e.preventDefault();
    target.scrollIntoView({ behavior: "smooth", block: "start" });
    // Reflect the active section in the URL hash; keep the ?doc= param so a
    // refresh lands on the same file AND section. replaceState — no history spam.
    window.history.replaceState(null, "", `${window.location.pathname}${window.location.search}#${id}`);
  };

  // On first mount (content is already in the DOM — Reader only renders once the
  // doc has loaded), restore the section from location.hash if present. Mount-only
  // so the 3s content poll can't yank the reader back mid-read.
  useEffect(() => {
    const hash = window.location.hash;
    if (hash.length < 2) return;
    const id = decodeURIComponent(hash.slice(1));
    const raf = requestAnimationFrame(() => {
      document.getElementById(id)?.scrollIntoView({ block: "start" });
    });
    return () => cancelAnimationFrame(raf);
  }, []);

  return (
    <div ref={rootRef} className="reader markdown-body" onClick={onClick}>
      <MarkdownView content={content} />
    </div>
  );
}
