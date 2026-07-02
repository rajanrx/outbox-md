package rag

import (
	"context"
	"math"
	"sort"
	"strings"
)

// Result is one retrieval hit: the source doc + heading for citation, a snippet,
// a blended score (higher = more relevant), and the Source that produced it.
type Result struct {
	DocPath string
	Heading string
	Snippet string
	Score   float64
	Source  string // "semantic" | "keyword" | "hybrid"
}

// Snippet length for a result preview (characters).
const snippetChars = 240

// Blend weights when both signals are present (design §5: hybrid). Vector recall
// leads; keyword refines.
const (
	weightVector  = 0.6
	weightLexical = 0.4
)

// Searcher is the read path over the KB store. When an embedder is configured and
// healthy it blends cosine-over-vectors with the FTS5/BM25 lexical layer; with no
// embedder (or one that is failing) it serves keyword-only. Scoped to a single
// project (kb_scope: all is a later slice — this slice is always project-scoped).
type Searcher struct {
	Store    *Store
	Embedder Embedder // may be nil ⇒ keyword-only
	Mode     *ModeTracker
}

// NewSearcher wires a searcher. embedder may be nil; mode may be nil.
func NewSearcher(store *Store, embedder Embedder, mode *ModeTracker) *Searcher {
	if mode == nil {
		mode = NewModeTracker(embedder != nil, nil)
	}
	return &Searcher{Store: store, Embedder: embedder, Mode: mode}
}

// Search returns the top-k hits for query within project. It is hybrid when an
// embedder is present and the query embeds successfully (cosine blended with
// BM25); otherwise it degrades to keyword-only — never an error just because
// there is no embedder. A query-embed failure flips the mode to degraded and
// falls back to keyword search rather than failing the call.
func (s *Searcher) Search(project, query string, k int) ([]Result, error) {
	if k <= 0 {
		k = 5
	}
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}

	lexical, err := s.Store.LexicalSearch(project, query, k*4)
	if err != nil {
		return nil, err
	}

	qvec := s.embedQuery(context.Background(), query)
	if qvec == nil {
		// Keyword-only path: no embedder, or the query embed failed (degraded).
		return topKeyword(lexical, k), nil
	}

	vecs, err := s.Store.VectorChunks(project, s.Embedder.ID(), s.Embedder.Dims())
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		// Embedder healthy but nothing vector-indexed yet for this embedder — serve
		// keyword results so retrieval still works during back-fill.
		return topKeyword(lexical, k), nil
	}
	return blend(qvec, vecs, lexical, k), nil
}

// embedQuery embeds the query with the configured embedder, updating the mode on
// success/failure. Returns nil when there is no embedder or the embed failed.
func (s *Searcher) embedQuery(ctx context.Context, query string) []float32 {
	if s.Embedder == nil {
		return nil
	}
	vecs, err := s.Embedder.Embed(ctx, []string{query})
	if err != nil || len(vecs) != 1 {
		if s.Mode != nil {
			s.Mode.MarkDegraded()
		}
		return nil
	}
	if s.Mode != nil {
		s.Mode.MarkHealthy()
	}
	return vecs[0]
}

// topKeyword projects lexical hits to Results (min-max normalised score) and caps
// at k.
func topKeyword(hits []LexicalHit, k int) []Result {
	norm := normaliseLexical(hits)
	out := make([]Result, 0, len(hits))
	for _, h := range hits {
		out = append(out, Result{
			DocPath: h.DocPath,
			Heading: h.Heading,
			Snippet: snippet(h.Text),
			Score:   norm[h.ID],
			Source:  "keyword",
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > k {
		out = out[:k]
	}
	return out
}

// blend merges cosine similarity over vectors with normalised lexical scores.
// Every vector-indexed chunk contributes its cosine; a chunk also present in the
// lexical hits adds the keyword weight. Chunks that are ONLY lexical hits (rare —
// e.g. a chunk embedded by a different, stale embedder) still surface via the
// keyword contribution. Higher blended score ranks first.
func blend(qvec []float32, vecs []VectorChunk, lexical []LexicalHit, k int) []Result {
	lexNorm := normaliseLexical(lexical)
	byID := map[string]*Result{}
	order := []string{}

	get := func(id, docPath, heading, text string) *Result {
		if r, ok := byID[id]; ok {
			return r
		}
		r := &Result{DocPath: docPath, Heading: heading, Snippet: snippet(text)}
		byID[id] = r
		order = append(order, id)
		return r
	}

	for _, vc := range vecs {
		cos := cosineNorm(qvec, vc.Embedding) // mapped to [0,1]
		r := get(vc.ID, vc.DocPath, vc.Heading, vc.Text)
		r.Score += weightVector * cos
		r.Source = mergeSource(r.Source, "semantic")
	}
	for _, h := range lexical {
		r := get(h.ID, h.DocPath, h.Heading, h.Text)
		r.Score += weightLexical * lexNorm[h.ID]
		r.Source = mergeSource(r.Source, "keyword")
	}

	out := make([]Result, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > k {
		out = out[:k]
	}
	return out
}

// mergeSource combines contribution labels: a hit from both signals is "hybrid".
func mergeSource(existing, add string) string {
	if existing == "" || existing == add {
		return add
	}
	return "hybrid"
}

// normaliseLexical min-max scales lexical scores to [0,1] keyed by chunk id. A
// single hit (or all-equal scores) maps to 1.0.
func normaliseLexical(hits []LexicalHit) map[string]float64 {
	out := make(map[string]float64, len(hits))
	if len(hits) == 0 {
		return out
	}
	min, max := hits[0].Score, hits[0].Score
	for _, h := range hits {
		if h.Score < min {
			min = h.Score
		}
		if h.Score > max {
			max = h.Score
		}
	}
	span := max - min
	for _, h := range hits {
		if span == 0 {
			out[h.ID] = 1
		} else {
			out[h.ID] = (h.Score - min) / span
		}
	}
	return out
}

// cosineNorm returns cosine similarity mapped from [-1,1] to [0,1]. Mismatched or
// zero-magnitude vectors score 0.
func cosineNorm(a, b []float32) float64 {
	return (cosine(a, b) + 1) / 2
}

func cosine(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// snippet trims a chunk to a preview, collapsing whitespace and cutting on a word
// boundary near the limit.
func snippet(text string) string {
	s := strings.Join(strings.Fields(text), " ")
	if len(s) <= snippetChars {
		return s
	}
	cut := snippetChars
	if idx := strings.LastIndex(s[:snippetChars], " "); idx > snippetChars/2 {
		cut = idx
	}
	return strings.TrimSpace(s[:cut]) + "…"
}
