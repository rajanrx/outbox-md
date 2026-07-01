import { type Row } from "./diff";

const SIGN = { eq: " ", ins: "+", del: "−", gap: "" } as const;

// DiffRows renders a list of unified-diff rows with the GitHub-style .diff/.drow
// styling. It is the single presentational primitive shared by the inline
// suggestion panel, the review modal's "This change", and each folder-changes
// file section — so every diff surface looks identical.
export function DiffRows({ rows }: { rows: Row[] }) {
  return (
    <div className="diff">
      {rows.map((r, i) => (
        <div key={i} className={`drow ${r.op}`}>
          <span className="sign">{SIGN[r.op]}</span>
          <span className="text">{r.text || " "}</span>
        </div>
      ))}
    </div>
  );
}

// counts tallies inserted/deleted lines for a `+N −M` summary badge.
export function counts(rows: Row[]): { ins: number; del: number } {
  let ins = 0;
  let del = 0;
  for (const r of rows) {
    if (r.op === "ins") ins++;
    else if (r.op === "del") del++;
  }
  return { ins, del };
}
