import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import rehypeHighlight from "rehype-highlight";
import "highlight.js/styles/github.css";
import { rehypeSourcePos } from "./rehypeSourcePos";
import { rehypeHeadingIds } from "./rehypeHeadingIds";
import { MermaidBlock } from "./mermaid";
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
    const target = document.getElementById(decodeURIComponent(href.slice(1)));
    if (!target) return;
    e.preventDefault();
    target.scrollIntoView({ behavior: "smooth", block: "start" });
  };

  return (
    <div ref={rootRef} className="reader markdown-body" onClick={onClick}>
      <Markdown
        remarkPlugins={[remarkGfm]}
        rehypePlugins={[rehypeSourcePos, rehypeHeadingIds, rehypeHighlight]}
        components={{
          code(props: any) {
            const cls: string = props.className || "";
            const text = String(props.children ?? "");
            if (cls.includes("language-mermaid")) return <MermaidBlock chart={text} />;
            return <code className={cls}>{props.children}</code>;
          },
        }}
      >
        {content}
      </Markdown>
    </div>
  );
}
