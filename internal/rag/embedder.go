// Package rag is the local retrieval knowledge-base core (design §5): a
// heading-aware Markdown chunker, a chunk+vector+FTS5 SQLite store, an index
// builder, and hybrid (vector + keyword) search behind a retrieval-mode model.
//
// The package is self-contained and injectable: an Embedder and a *sql.DB are
// passed in, so the server, watcher, and MCP layers (the 3B/3C slices) can wire
// it in without reaching into internals. Vectors are stored as float32 BLOBs and
// scored with brute-force cosine in Go — a project holds hundreds to low
// thousands of chunks, so a flat scan is sub-millisecond and needs no C loadable
// extension (pure-Go modernc.org/sqlite cannot load sqlite-vec). FTS5 (which
// modernc ships) is the always-on lexical layer, kept outside the Embedder
// interface.
package rag

import (
	"context"

	"github.com/rajanrx/outbox-md/internal/config"
)

// Embedder turns text into vectors. Every embedder — the shell-out embed_cmd,
// the future hosted embed_api, an in-process ONNX model — sits behind this one
// narrow interface, so the indexer, watcher re-embed pass, and search depend only
// on it (design §5.2). Swapping embedders is a re-index, not a rewrite: the index
// is keyed by ID()+Dims() because vectors from different models are not
// comparable.
type Embedder interface {
	// ID is a stable identity for the embedder configuration. It MUST change when
	// the configuration changes (e.g. a different command), so a config change
	// invalidates the old vectors and triggers a re-index.
	ID() string
	// Dims is the vector dimensionality. It is discovered from the first successful
	// Embed call; before any successful embed it may report 0.
	Dims() int
	// Embed returns one vector per input text, in order. A nil/empty input yields a
	// nil/empty result. Any failure returns a non-nil error and no partial vectors.
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// NewEmbedder is the config-driven factory (design §5.2). This slice ships ONE
// implementation, embed_cmd. It returns (nil, nil) when no embedder is configured
// — the caller then runs keyword-only over FTS5; a missing embedder is a
// supported mode, never an error. embed_api is parsed by config but its
// implementation lands in a later slice; when it is configured (and embed_cmd is
// not) this returns (nil, nil) with the caller degrading to FTS5, so the presence
// of an embed_api block never hard-fails startup.
func NewEmbedder(cfg config.Config) (Embedder, error) {
	if cmd := trimSpace(cfg.EmbedCmd); cmd != "" {
		return NewCmdEmbedder(cmd), nil
	}
	// embed_api is reserved for a later slice; with no embed_cmd we fall through to
	// no embedder (FTS5-only) rather than failing. NOTE for 3C: spec §5.2 says
	// embed_api OVERRIDES embed_cmd when set — when the API embedder is implemented,
	// check cfg.EmbedAPI.Configured() BEFORE embed_cmd here and return it first.
	return nil, nil
}
