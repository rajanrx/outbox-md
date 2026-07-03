package rag

import (
	"strings"
	"testing"
)

func TestChunkMarkdownSplitsByHeading(t *testing.T) {
	doc := `# Title

Intro paragraph under the title.

## Alpha

Alpha body paragraph one.

Alpha body paragraph two.

## Beta

Beta body paragraph.`

	chunks := ChunkMarkdown("docs/x.md", doc)
	if len(chunks) < 3 {
		t.Fatalf("got %d chunks, want >=3 (one per heading section)", len(chunks))
	}
	// Every chunk carries the doc path and an ascending ordinal.
	for i, c := range chunks {
		if c.DocPath != "docs/x.md" {
			t.Fatalf("chunk %d docPath = %q", i, c.DocPath)
		}
		if c.Ord != i {
			t.Fatalf("chunk %d ord = %d, want %d", i, c.Ord, i)
		}
	}
	// Headings present and scoped: an Alpha chunk holds Alpha text, not Beta.
	var sawAlpha, sawBeta bool
	for _, c := range chunks {
		switch c.Heading {
		case "Alpha":
			sawAlpha = true
			if strings.Contains(c.Text, "Beta body") {
				t.Fatal("Alpha chunk leaked Beta content")
			}
		case "Beta":
			sawBeta = true
			if strings.Contains(c.Text, "Alpha body") {
				t.Fatal("Beta chunk leaked Alpha content")
			}
		}
	}
	if !sawAlpha || !sawBeta {
		t.Fatalf("missing heading-scoped chunks: alpha=%v beta=%v", sawAlpha, sawBeta)
	}
}

func TestChunkMarkdownRespectsBudget(t *testing.T) {
	// A section far larger than the budget must split into multiple bounded chunks.
	para := strings.Repeat("lorem ipsum dolor sit amet ", 400) // ~10.8k chars
	doc := "## Big\n\n" + para
	chunks := ChunkMarkdown("d.md", doc)
	if len(chunks) < 2 {
		t.Fatalf("oversized section produced %d chunks, want split", len(chunks))
	}
	for i, c := range chunks {
		// Allow a little slack for overlap/joining, but stay in the same order of
		// magnitude as the budget — never the whole 10k in one chunk.
		if len(c.Text) > MaxChunkChars+ChunkOverlapChars+50 {
			t.Fatalf("chunk %d len %d exceeds budget %d", i, len(c.Text), MaxChunkChars)
		}
	}
}

func TestChunkMarkdownEmpty(t *testing.T) {
	if got := ChunkMarkdown("d.md", "   \n\n  "); len(got) != 0 {
		t.Fatalf("empty doc produced %d chunks, want 0", len(got))
	}
}

func TestChunkMarkdownPreHeadingContent(t *testing.T) {
	chunks := ChunkMarkdown("d.md", "Loose intro before any heading.\n\n# H\n\nBody.")
	if len(chunks) < 2 {
		t.Fatalf("got %d chunks", len(chunks))
	}
	if chunks[0].Heading != "" {
		t.Fatalf("pre-heading chunk heading = %q, want empty", chunks[0].Heading)
	}
	if !strings.Contains(chunks[0].Text, "Loose intro") {
		t.Fatalf("pre-heading chunk text = %q", chunks[0].Text)
	}
}
