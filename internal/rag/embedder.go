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

// NewEmbedder is the config-driven factory (design §5.2). It picks embed_api OVER
// embed_cmd when the API block is configured (embed_api set ⇒ use it), then falls
// back to embed_cmd, then to no embedder. It returns (nil, nil) when nothing is
// configured — or when embed_api is configured but its key env is empty — so the
// caller runs keyword-only over FTS5; a missing/unkeyed embedder is a supported
// mode, never an error, and never hard-fails startup.
func NewEmbedder(cfg config.Config) (Embedder, error) {
	if cfg.EmbedAPI.Configured() {
		emb, err := NewAPIEmbedder(cfg.EmbedAPI)
		if err != nil {
			return nil, err
		}
		if emb == nil {
			// embed_api set but its key env is empty ⇒ degrade to FTS5. embed_api
			// overrides embed_cmd, so we do NOT fall through to embed_cmd here.
			return nil, nil
		}
		return emb, nil
	}
	if cmd := trimSpace(cfg.EmbedCmd); cmd != "" {
		return NewCmdEmbedder(cmd), nil
	}
	return nil, nil
}
