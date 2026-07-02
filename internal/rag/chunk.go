package rag

import (
	"regexp"
	"strings"
)

// Chunk is a retrievable slice of a document: its text, the nearest preceding
// heading, and its ordinal within the doc. DocPath is the doc's key (matching the
// store's doc_path). Chunks carry enough to cite back to the source (design §3.5).
type Chunk struct {
	DocPath string
	Heading string
	Ord     int
	Text    string
}

// Chunking budget (characters, a stand-in for a token budget — ~4 chars/token, so
// ~1200 chars ≈ ~300 tokens, comfortably inside any embedding model's window).
const (
	// MaxChunkChars bounds a chunk; a section longer than this is split on
	// paragraph boundaries, and a single oversized paragraph is hard-split.
	MaxChunkChars = 1200
	// ChunkOverlapChars is the trailing overlap carried into the next chunk so a
	// span straddling a boundary is still retrievable from both sides.
	ChunkOverlapChars = 150
)

// headingRe matches a Markdown ATX heading line (# … ######), capturing the text.
var headingRe = regexp.MustCompile(`^(#{1,6})\s+(.+?)\s*#*\s*$`)

// ChunkMarkdown splits a Markdown document into heading-scoped chunks. It walks
// the doc heading by heading; the body under each heading is packed into chunks
// bounded by MaxChunkChars, splitting only on paragraph (blank-line) boundaries so
// paragraphs stay whole, with ChunkOverlapChars of trailing context carried into
// the next chunk. A paragraph larger than the budget is hard-split. Each chunk
// records the nearest preceding heading and a doc-wide ordinal. Content before the
// first heading is chunked under an empty heading. Empty/whitespace docs yield no
// chunks.
func ChunkMarkdown(docPath, content string) []Chunk {
	sections := splitSections(content)
	var chunks []Chunk
	ord := 0
	for _, sec := range sections {
		for _, body := range packSection(sec.body) {
			chunks = append(chunks, Chunk{
				DocPath: docPath,
				Heading: sec.heading,
				Ord:     ord,
				Text:    body,
			})
			ord++
		}
	}
	return chunks
}

type section struct {
	heading string
	body    string
}

// splitSections breaks the doc at heading boundaries. Each section's body is the
// text between its heading and the next heading (the heading line itself is not
// part of the body). A run of leading content before any heading becomes a
// section with an empty heading.
func splitSections(content string) []section {
	lines := strings.Split(content, "\n")
	var sections []section
	cur := section{heading: ""}
	var body []string
	flush := func() {
		text := strings.TrimSpace(strings.Join(body, "\n"))
		if text != "" {
			cur.body = text
			sections = append(sections, cur)
		}
		body = body[:0]
	}
	for _, line := range lines {
		if m := headingRe.FindStringSubmatch(line); m != nil {
			flush()
			cur = section{heading: strings.TrimSpace(m[2])}
			continue
		}
		body = append(body, line)
	}
	flush()
	return sections
}

// packSection packs a section body into chunk-sized pieces on paragraph
// boundaries, carrying ChunkOverlapChars of trailing overlap between adjacent
// chunks. A single paragraph over the budget is hard-split.
func packSection(body string) []string {
	paras := splitParagraphs(body)
	var chunks []string
	var cur strings.Builder
	appendPara := func(p string) {
		if cur.Len() > 0 {
			cur.WriteString("\n\n")
		}
		cur.WriteString(p)
	}
	flush := func() {
		if cur.Len() == 0 {
			return
		}
		text := strings.TrimSpace(cur.String())
		chunks = append(chunks, text)
		cur.Reset()
		if ChunkOverlapChars > 0 {
			cur.WriteString(overlapTail(text, ChunkOverlapChars))
		}
	}
	for _, p := range paras {
		if len(p) > MaxChunkChars {
			// Oversized paragraph: flush what we have, then hard-split the paragraph.
			flush()
			cur.Reset()
			for _, piece := range hardSplit(p, MaxChunkChars) {
				chunks = append(chunks, piece)
			}
			continue
		}
		if cur.Len() > 0 && cur.Len()+len(p)+2 > MaxChunkChars {
			flush()
		}
		appendPara(p)
	}
	// Final flush without seeding overlap (nothing follows).
	if cur.Len() > 0 {
		chunks = append(chunks, strings.TrimSpace(cur.String()))
	}
	return dropEmpty(chunks)
}

// splitParagraphs splits on blank lines, trimming each paragraph.
func splitParagraphs(body string) []string {
	raw := regexp.MustCompile(`\n[ \t]*\n`).Split(body, -1)
	var out []string
	for _, p := range raw {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// hardSplit chops an oversized string into <=max pieces, preferring a whitespace
// boundary near the limit so words are not cut mid-token where possible.
func hardSplit(s string, max int) []string {
	var out []string
	for len(s) > max {
		cut := max
		if idx := strings.LastIndexAny(s[:max], " \n\t"); idx > max/2 {
			cut = idx
		}
		out = append(out, strings.TrimSpace(s[:cut]))
		s = strings.TrimSpace(s[cut:])
	}
	if s != "" {
		out = append(out, s)
	}
	return out
}

// overlapTail returns up to n trailing characters of s, snapped to a word
// boundary so the carried overlap starts on a whole word.
func overlapTail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	tail := s[len(s)-n:]
	if idx := strings.IndexAny(tail, " \n\t"); idx >= 0 && idx < len(tail)-1 {
		tail = tail[idx+1:]
	}
	return strings.TrimSpace(tail)
}

func dropEmpty(in []string) []string {
	out := in[:0]
	for _, s := range in {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}
