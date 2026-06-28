import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import rehypeHighlight from "rehype-highlight";
import "highlight.js/styles/github-dark.css";
import { rehypeSourcePos } from "./rehypeSourcePos";
import { MermaidBlock } from "./mermaid";
import "./reader.css";

export function Reader({ content, rootRef }: {
  content: string;
  rootRef: React.RefObject<HTMLDivElement | null>;
}) {
  return (
    <div ref={rootRef} className="reader markdown-body">
      <Markdown
        remarkPlugins={[remarkGfm]}
        rehypePlugins={[rehypeSourcePos, rehypeHighlight]}
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
