import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import rehypeHighlight from "rehype-highlight";
import "highlight.js/styles/github.css";
import { rehypeSourcePos } from "./rehypeSourcePos";
import { rehypeHeadingIds } from "./rehypeHeadingIds";
import { MermaidBlock } from "./mermaid";
import "./reader.css";

// MarkdownView is the single markdown renderer shared by the reading pane and the
// diff modal's "Rendered" view. It bundles the remark/rehype pipeline (GFM,
// source-position stamps for anchoring, GitHub-style heading ids, syntax
// highlighting) plus the `code` override that routes ```mermaid fences to
// MermaidBlock. Keep this the ONE place that knows how a doc becomes HTML so the
// reader and the "see it rendered" diff view can never drift apart.
export function MarkdownView({ content }: { content: string }) {
  return (
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
  );
}
