package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/rajanrx/outbox-md/internal/domain"
	"github.com/rajanrx/outbox-md/internal/service"
	"github.com/rajanrx/outbox-md/internal/sse"
	"github.com/rajanrx/outbox-md/internal/store"
)

// NewAPI builds the JSON API.
func NewAPI(svc *service.Service, st *store.Store, hub *sse.Hub) http.Handler {
	mux := http.NewServeMux()

	// SSE stream of governance events for live UI updates. The browser opens this
	// once and refreshes on each event, replacing the old 3s poll (now a fallback).
	mux.HandleFunc("GET /api/events", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok || hub == nil {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		ch, unsub := hub.Subscribe()
		defer unsub()

		// Open the stream immediately so the client (and tests) know the
		// subscription is live, then heartbeat every 25s to keep proxies from
		// idling the connection out. Both are SSE comment lines the browser ignores.
		fmt.Fprint(w, ": connected\n\n")
		flusher.Flush()
		ping := time.NewTicker(25 * time.Second)
		defer ping.Stop()

		for {
			select {
			case <-r.Context().Done(): // client disconnected
				return
			case <-ping.C:
				fmt.Fprint(w, ": ping\n\n")
				flusher.Flush()
			case msg, ok := <-ch:
				if !ok {
					return
				}
				fmt.Fprintf(w, "event: %s\ndata: %s\n\n", msg.Event, msg.Data)
				flusher.Flush()
			}
		}
	})

	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, svc.Config(), nil)
	})

	// The projects being served: [{name, root, docs}]. Single-folder mode returns
	// one entry with an empty name; the UI hides the switcher when there is ≤1
	// project. The per-project agent command is deliberately NOT exposed here (the
	// UI never needs it). name is the root basename, so labels read as the repo
	// name (e.g. "outbox-md") rather than the served folder (e.g. "docs").
	mux.HandleFunc("GET /api/projects", func(w http.ResponseWriter, _ *http.Request) {
		type projectDTO struct {
			Name string `json:"name"`
			Root string `json:"root"`
			Docs string `json:"docs"`
		}
		src := svc.Projects()
		out := make([]projectDTO, 0, len(src))
		for _, p := range src {
			docs := p.Docs
			if docs == "" {
				docs = "."
			}
			out = append(out, projectDTO{Name: p.Name, Root: p.Root, Docs: docs})
		}
		writeJSON(w, out, nil)
	})

	// Read-only folder view built from outbox-md's OWN data: every doc across the
	// project that currently has a pending (proposed) suggestion, each as a
	// current-vs-proposed pair the UI renders with its single-file diff. No git —
	// always available.
	mux.HandleFunc("GET /api/suggestions/pending", func(w http.ResponseWriter, _ *http.Request) {
		pending, err := st.ListPendingSuggestions()
		if err == nil {
			if svc.SourcesRestricted() {
				kept := pending[:0:0]
				for _, p := range pending {
					if svc.ProjectServes(p.Project, p.Path) {
						kept = append(kept, p)
					}
				}
				pending = kept
			}
		}
		writeJSON(w, pending, err)
	})

	mux.HandleFunc("GET /api/docs", func(w http.ResponseWriter, _ *http.Request) {
		docs, err := st.ListDocuments()
		if err == nil {
			if svc.SourcesRestricted() {
				kept := docs[:0:0]
				for _, d := range docs {
					if svc.ProjectServes(d.Project, d.Path) {
						kept = append(kept, d)
					}
				}
				docs = kept
			}
		}
		writeJSON(w, docs, err)
	})

	mux.HandleFunc("GET /api/docs/{id}", func(w http.ResponseWriter, r *http.Request) {
		doc, err := st.GetDocument(r.PathValue("id"))
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		ver, err := st.GetVersion(doc.CurrentVersionID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		comments, err := st.ListComments(doc.ID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		baseline := ""
		if doc.ApprovedVersionID != "" {
			if bv, err := st.GetVersion(doc.ApprovedVersionID); err == nil {
				baseline = bv.Content
			}
		}
		writeJSON(w, map[string]any{
			"document": doc, "content": ver.Content, "comments": comments, "baselineContent": baseline,
		}, nil)
	})

	mux.HandleFunc("GET /api/docs/{id}/log", func(w http.ResponseWriter, r *http.Request) {
		log, err := st.ListDecisionLog(r.PathValue("id"))
		writeJSON(w, log, err)
	})

	mux.HandleFunc("POST /api/docs/{id}/approve", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Note string `json:"note"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in) // body/note optional
		a, err := svc.Approve(r.PathValue("id"), in.Note)
		if err != nil {
			writeJSONError(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, a, nil)
	})
	mux.HandleFunc("POST /api/docs/{id}/reapprove", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Note string `json:"note"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		a, err := svc.Reapprove(r.PathValue("id"), in.Note)
		if err != nil {
			writeJSONError(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, a, nil)
	})

	mux.HandleFunc("POST /api/docs/{id}/comments", func(w http.ResponseWriter, r *http.Request) {
		var in domain.Anchor
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		c, err := svc.PostComment(r.PathValue("id"), in, "human")
		writeJSON(w, c, err)
	})

	mux.HandleFunc("GET /api/comments/{id}/suggestion", func(w http.ResponseWriter, r *http.Request) {
		sg, ok, err := st.GetSuggestionByComment(r.PathValue("id"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !ok {
			http.Error(w, "no suggestion", http.StatusNotFound)
			return
		}
		writeJSON(w, sg, nil)
	})

	// Council read model (roadmap §3): the candidate set + candidates + synthesis.
	mux.HandleFunc("GET /api/comments/{id}/candidates", func(w http.ResponseWriter, r *http.Request) {
		view, err := svc.ListCandidates(r.PathValue("id"))
		if err != nil {
			if errors.Is(err, service.ErrNoCandidateSet) {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, view, nil)
	})

	// Human-only pick. Like resolve/approve, the actor is server-set to the local
	// human (never taken from the request body), so it cannot be spoofed — and
	// there is deliberately no MCP equivalent.
	mux.HandleFunc("POST /api/comments/{id}/candidates/{cid}/pick", func(w http.ResponseWriter, r *http.Request) {
		cand, err := svc.PickCandidate(r.PathValue("id"), r.PathValue("cid"), service.LocalHuman)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, cand, nil)
	})

	mux.HandleFunc("POST /api/comments/{id}/accept", func(w http.ResponseWriter, r *http.Request) {
		v, err := svc.Accept(r.PathValue("id"))
		writeJSON(w, map[string]any{"version": v}, err)
	})

	// Dev-only: simulate an agent over HTTP so the full loop is exercisable
	// without a live MCP agent. Enabled with OUTBOX_DEV=1.
	if os.Getenv("OUTBOX_DEV") == "1" {
		mux.HandleFunc("POST /api/dev/claim", func(w http.ResponseWriter, r *http.Request) {
			var in struct {
				CommentIDs []string `json:"commentIds"`
			}
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			// The route guard is path-based; these dev endpoints carry comment ids
			// in the body, so enforce the whitelist here too.
			for _, id := range in.CommentIDs {
				if !commentDocServed(svc, st, id) {
					http.Error(w, "not found", http.StatusNotFound)
					return
				}
			}
			tok, err := svc.Claim(in.CommentIDs, "dev-agent")
			writeJSON(w, map[string]any{"token": tok}, err)
		})
		mux.HandleFunc("POST /api/dev/propose", func(w http.ResponseWriter, r *http.Request) {
			var in struct {
				CommentID string `json:"commentId"`
				Token     string `json:"token"`
				Content   string `json:"content"`
			}
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if !commentDocServed(svc, st, in.CommentID) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			sg, err := svc.Propose(in.CommentID, in.Token, in.Content, "dev-agent")
			writeJSON(w, sg, err)
		})
	}

	mux.HandleFunc("GET /api/comments/{id}/thread", func(w http.ResponseWriter, r *http.Request) {
		msgs, err := st.ListThread(r.PathValue("id"))
		writeJSON(w, msgs, err)
	})
	mux.HandleFunc("POST /api/comments/{id}/reply", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Body string `json:"body"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		m, err := svc.HumanReply(r.PathValue("id"), in.Body)
		writeJSON(w, m, err)
	})
	// Agent-facing (api-mode runner): mark a claimed comment as being worked on so
	// the human sees an "AI processing…" indicator live. Requires the claim token;
	// ttlSeconds is optional (<=0 uses the server default). Ephemeral — writes no
	// file and changes no status. Mirrors the MCP mark_processing tool.
	mux.HandleFunc("POST /api/comments/{id}/processing", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Token      string `json:"token"`
			TTLSeconds int    `json:"ttlSeconds"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		until, err := svc.MarkProcessing(r.PathValue("id"), in.Token, time.Duration(in.TTLSeconds)*time.Second)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, map[string]any{"processingUntil": until.Format(time.RFC3339)}, nil)
	})

	// Runner-facing: the instant "received" ack. The runner POSTs this the moment
	// a webhook lands — before the agent claims — so the "AI processing…" badge
	// appears within ~1s (and shows even if the agent dies before claiming).
	// Deliberately UNTOKENED and body-less: no agent has claimed yet, so there is
	// no claim token to present. Kept open on purpose — it is a low-risk, ephemeral
	// hint (self-expires in ReceivedTTL) that writes no file and changes no status.
	// The tokened /processing endpoint extends it once the agent is working.
	mux.HandleFunc("POST /api/comments/{id}/received", func(w http.ResponseWriter, r *http.Request) {
		until, err := svc.MarkReceived(r.PathValue("id"))
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, map[string]any{"processingUntil": until.Format(time.RFC3339)}, nil)
	})

	mux.HandleFunc("POST /api/comments/{id}/resolve", func(w http.ResponseWriter, r *http.Request) {
		// Caller identity is server-set (the single local human); it is never
		// taken from the request body, so it cannot be spoofed.
		if err := svc.Resolve(r.PathValue("id")); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]any{"ok": true}, nil)
	})

	mux.HandleFunc("POST /api/comments/{id}/reject", func(w http.ResponseWriter, r *http.Request) {
		if err := svc.RejectSuggestion(r.PathValue("id")); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]any{"ok": true}, nil)
	})

	// Enforce the sources whitelist uniformly across every doc- and
	// comment-scoped route (GET/POST /api/docs/{id}/…, /api/comments/{id}/…),
	// so a stale hidden-doc id can't read or mutate through any single endpoint.
	// A single guard wrapping the mux can't miss a route the way per-handler
	// checks could. No-op (zero extra lookups) when no whitelist is configured.
	return guardSources(mux, svc, st)
}

// guardSources 404s any doc- or comment-scoped request whose target doc is
// outside the active sources whitelist, before the request reaches its handler.
func guardSources(next http.Handler, svc *service.Service, st *store.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if svc.SourcesRestricted() {
			if project, docPath, scoped := targetDocPath(st, r.URL.Path); scoped && !svc.ProjectServes(project, docPath) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// targetDocPath resolves the project and doc path a request targets when the
// path is doc-scoped (/api/docs/{id}…) or comment-scoped (/api/comments/{id}…).
// scoped is true for those paths (even when the id is unknown — then project and
// docPath are both "", which ProjectServes rejects for any restricted config,
// matching the 404 the handler would return anyway); false for non-scoped paths
// (list, config, events, suggestions/pending), which do their own filtering.
func targetDocPath(st *store.Store, path string) (project, docPath string, scoped bool) {
	idAfter := func(prefix string) (string, bool) {
		if !strings.HasPrefix(path, prefix) {
			return "", false
		}
		id, _, _ := strings.Cut(strings.TrimPrefix(path, prefix), "/")
		return id, id != ""
	}
	if id, ok := idAfter("/api/docs/"); ok {
		if doc, err := st.GetDocument(id); err == nil {
			return doc.Project, doc.Path, true
		}
		return "", "", true
	}
	if id, ok := idAfter("/api/comments/"); ok {
		if c, err := st.GetComment(id); err == nil {
			if doc, err := st.GetDocument(c.DocID); err == nil {
				return doc.Project, doc.Path, true
			}
		}
		return "", "", true
	}
	return "", "", false
}

// commentDocServed reports whether commentID's parent doc is inside the active
// sources whitelist. Used by the body-carrying dev endpoints, which the
// path-based guardSources cannot reach. No whitelist → always true.
func commentDocServed(svc *service.Service, st *store.Store, commentID string) bool {
	if !svc.SourcesRestricted() {
		return true
	}
	c, err := st.GetComment(commentID)
	if err != nil {
		return false
	}
	doc, err := st.GetDocument(c.DocID)
	if err != nil {
		return false
	}
	return svc.ProjectServes(doc.Project, doc.Path)
}

func writeJSON(w http.ResponseWriter, v any, err error) {
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// writeJSONError renders an error as a JSON body {"error": "..."} with the given
// status, so clients (the web UI) can surface the message rather than a raw
// text/plain body.
func writeJSONError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
