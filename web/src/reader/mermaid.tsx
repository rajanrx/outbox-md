import { useEffect, useRef, useState } from "react";
import mermaid from "mermaid";

mermaid.initialize({ startOnLoad: false, theme: "default", securityLevel: "strict" });

export function MermaidBlock({ chart }: { chart: string }) {
  const [svg, setSvg] = useState<string>("");
  const id = useRef("m" + Math.random().toString(36).slice(2)).current;
  useEffect(() => {
    let alive = true;
    mermaid
      .render(id, chart)
      .then((r) => alive && setSvg(r.svg))
      .catch(() => alive && setSvg("<pre>mermaid error</pre>"));
    return () => { alive = false; };
  }, [chart, id]);
  return <div className="mermaid-block" data-mermaid dangerouslySetInnerHTML={{ __html: svg }} />;
}
