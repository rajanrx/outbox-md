package rag

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log"

	"github.com/rajanrx/outbox-md/internal/domain"
)

// Doc is one served document for BuildAll: its project, key path, and content.
// The caller passes the served docs-union (respecting the whitelist) so the RAG
// index never couples to serve internals.
type Doc struct {
	Project string
	Path    string
	Content string
}

// Indexer builds and maintains the KB index. It holds the store, an optional
// Embedder (nil ⇒ keyword-only), and the retrieval-mode tracker it updates on
// embed success/failure. It is the write path that the watcher (3B) and startup
// reconciliation drive; search reads the same store.
type Indexer struct {
	Store    *Store
	Embedder Embedder // may be nil ⇒ text/FTS5 only
	Mode     *ModeTracker
	logf     func(format string, args ...any)
}

// NewIndexer wires an indexer. embedder may be nil. mode may be nil (a tracker is
// created from whether an embedder was supplied). logf may be nil.
func NewIndexer(store *Store, embedder Embedder, mode *ModeTracker, logf func(format string, args ...any)) *Indexer {
	if logf == nil {
		logf = log.Printf
	}
	if mode == nil {
		mode = NewModeTracker(embedder != nil, logf)
	}
	return &Indexer{Store: store, Embedder: embedder, Mode: mode, logf: logf}
}

// Reindex re-chunks a single doc and replaces its stored chunks. Text + FTS5 are
// ALWAYS written; embeddings are added when an embedder is configured and the
// embed call succeeds. An embed failure is NON-BLOCKING (design §5.1): the chunks
// are still indexed as text (embedding-stale), the mode flips to degraded, and nil
// is returned so a batch keeps going — only a store error propagates. On success
// with an embedder the mode is marked healthy (clears a prior degraded state).
func (ix *Indexer) Reindex(ctx context.Context, project, docPath, content string) error {
	_, err := ix.reindex(ctx, project, docPath, content)
	return err
}

// ReindexIfChanged re-indexes a doc ONLY when its content differs from what the
// index already holds (the stored per-doc content hash). It lets the watcher drive
// re-indexing on EVERY doc event — including a service-owned write (Accept /
// Reapprove) the governance store already absorbed, where SyncFile reports no
// change — so the KB never serves stale content until restart, WITHOUT a redundant
// re-embed when the on-disk bytes are truly unchanged (e.g. an editor touch). A doc
// the index has never seen is indexed.
func (ix *Indexer) ReindexIfChanged(ctx context.Context, project, docPath, content string) error {
	if h, ok, err := ix.Store.DocContentHash(project, docPath); err == nil && ok && h == contentHash(content) {
		return nil
	}
	return ix.Reindex(ctx, project, docPath, content)
}

// reindex is Reindex plus a report of whether the embed step failed (so BuildAll
// can settle the aggregate mode). embedFailed is false when there is no embedder.
func (ix *Indexer) reindex(ctx context.Context, project, docPath, content string) (embedFailed bool, err error) {
	chunks := ChunkMarkdown(docPath, content)
	hash := contentHash(content)
	rows := make([]ChunkRow, len(chunks))
	for i, c := range chunks {
		rows[i] = ChunkRow{
			ID:          domain.NewID(),
			Project:     project,
			DocPath:     docPath,
			Heading:     c.Heading,
			Ord:         c.Ord,
			Text:        c.Text,
			ContentHash: hash,
		}
	}

	if ix.Embedder != nil && len(rows) > 0 {
		texts := make([]string, len(rows))
		for i, r := range rows {
			texts[i] = r.Text
		}
		vecs, embErr := ix.Embedder.Embed(ctx, texts)
		switch {
		case embErr != nil:
			// Embed failed: index text/FTS5 only, mark stale + degraded, don't block.
			ix.logf("rag: embed failed for %s/%s, indexing keyword-only (retriable): %v", project, docPath, embErr)
			ix.Mode.MarkDegraded()
			embedFailed = true
		case len(vecs) == len(rows):
			id, dims := ix.Embedder.ID(), ix.Embedder.Dims()
			for i := range rows {
				rows[i].Embedding = vecs[i]
				rows[i].EmbedderID = id
				rows[i].Dims = dims
			}
			ix.Mode.MarkHealthy()
		default:
			ix.logf("rag: embedder returned %d vectors for %d chunks (%s/%s); indexing keyword-only",
				len(vecs), len(rows), project, docPath)
			ix.Mode.MarkDegraded()
			embedFailed = true
		}
	}

	return embedFailed, ix.Store.UpsertDoc(project, docPath, rows)
}

// BuildAll (re)indexes a set of served docs. It runs Reindex per doc, collecting
// any store error but continuing (a broken doc never blocks the rest). It sets the
// retrieval mode ONCE from the aggregate at the end — all embeds succeeded ⇒
// semantic, any embed failed ⇒ degraded, no embedder ⇒ keyword-only — so a batch
// emits a single settled mode rather than per-doc flapping.
func (ix *Indexer) BuildAll(ctx context.Context, docs []Doc) error {
	var firstErr error
	anyEmbedFail := false

	served := make(map[DocKey]bool, len(docs))
	for _, d := range docs {
		served[DocKey{Project: d.Project, Path: d.Path}] = true
		embedFailed, err := ix.reindex(ctx, d.Project, d.Path, d.Content)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		if embedFailed {
			anyEmbedFail = true
		}
	}

	// Prune: docs is the COMPLETE served set, so any doc still indexed but NOT in it
	// (removed from docs, hidden by narrowed sources, or deleted before startup) must
	// be dropped — otherwise it stays retrievable from the project-scoped store even
	// though it is no longer served. This makes the index exactly match the served set.
	if indexed, err := ix.Store.ListIndexedDocs(); err != nil {
		if firstErr == nil {
			firstErr = err
		}
	} else {
		for _, k := range indexed {
			if !served[k] {
				_ = ix.Store.DeleteDoc(k.Project, k.Path)
			}
		}
	}

	switch {
	case ix.Embedder == nil:
		ix.Mode.Set(ModeKeywordOnly)
	case anyEmbedFail:
		ix.Mode.Set(ModeDegraded)
	default:
		ix.Mode.Set(ModeSemantic)
	}
	return firstErr
}

// ReconcileStats is the one-line summary of a §5.1 startup reconciliation: how
// many served docs were re-embedded (stale/missing/embedder-changed), how many
// were already current, and how many stale docs were dropped from the index.
type ReconcileStats struct {
	Reindexed int
	Unchanged int
	Dropped   int
}

// Reconcile converges the index with the served docs-union at `serve` start
// (design §5.1), so offline edits and `git pull` need no manual rebuild. `docs`
// is the served set read FROM DISK (the governance store may hold stale content
// for a doc it already imported, so reconciliation must use on-disk bytes). For
// each served doc it re-embeds when the doc is missing, its content hash drifted,
// OR — when an embedder is configured — it carries no vectors for the CURRENT
// embedder (the install-then-restart upgrade: keyword-only chunks gain vectors
// once ollama is present). Docs still current are left untouched. Finally it
// drops chunks for any indexed doc no longer in the served set (deleted, renamed,
// or un-served), scanning every project that appears on disk OR in the index so a
// project that vanished entirely is cleaned too. The aggregate retrieval mode is
// settled once at the end, exactly like BuildAll. A per-doc store error is
// collected but never aborts the sweep.
func (ix *Indexer) Reconcile(ctx context.Context, docs []Doc) (ReconcileStats, error) {
	var stats ReconcileStats
	var firstErr error
	anyEmbedFail := false

	served := make(map[string]map[string]struct{})
	for _, d := range docs {
		if served[d.Project] == nil {
			served[d.Project] = make(map[string]struct{})
		}
		served[d.Project][d.Path] = struct{}{}

		if !ix.docCurrent(d.Project, d.Path, d.Content) {
			embedFailed, err := ix.reindex(ctx, d.Project, d.Path, d.Content)
			if err != nil && firstErr == nil {
				firstErr = err
			}
			if embedFailed {
				anyEmbedFail = true
			}
			stats.Reindexed++
		} else {
			stats.Unchanged++
		}
	}

	// Drop chunks for docs no longer served/present. Scan the union of projects on
	// disk and in the index so a wholly-removed project is cleaned, not just
	// per-doc deletions within a still-served project.
	projects := map[string]struct{}{}
	for p := range served {
		projects[p] = struct{}{}
	}
	if stored, err := ix.Store.ListProjects(); err == nil {
		for _, p := range stored {
			projects[p] = struct{}{}
		}
	} else if firstErr == nil {
		firstErr = err
	}
	for project := range projects {
		paths, err := ix.Store.ListDocPaths(project)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, path := range paths {
			if _, ok := served[project][path]; ok {
				continue
			}
			if err := ix.Store.DeleteDoc(project, path); err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			stats.Dropped++
		}
	}

	switch {
	case ix.Embedder == nil:
		ix.Mode.Set(ModeKeywordOnly)
	case anyEmbedFail:
		ix.Mode.Set(ModeDegraded)
	default:
		ix.Mode.Set(ModeSemantic)
	}
	ix.logf("rag: reconcile complete — %d re-embedded, %d unchanged, %d dropped",
		stats.Reindexed, stats.Unchanged, stats.Dropped)
	return stats, firstErr
}

// docCurrent reports whether the stored index for (project, docPath) already
// matches content AND is vector-current under the active embedder. False ⇒
// Reconcile re-embeds. A doc with no chunks is not current (never indexed); a
// hash drift is not current (offline edit); and — when an embedder is configured
// — a hash match with zero vectors for the current embedder is not current
// (was keyword-only, an embedder has since appeared).
func (ix *Indexer) docCurrent(project, docPath, content string) bool {
	stored, ok, err := ix.Store.DocContentHash(project, docPath)
	if err != nil || !ok {
		return false
	}
	if stored != contentHash(content) {
		return false
	}
	if ix.Embedder != nil {
		n, err := ix.Store.DocVectorCount(project, docPath, ix.Embedder.ID(), ix.Embedder.Dims())
		if err != nil || n == 0 {
			return false
		}
	}
	return true
}

// contentHash is the per-doc content hash used for §5.1 reconciliation (compare
// on-disk hash vs stored hash to decide re-embed). Full sha256 hex.
func contentHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}
