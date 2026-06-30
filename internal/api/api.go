package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/rajanrx/outbox-md/internal/domain"
	"github.com/rajanrx/outbox-md/internal/service"
	"github.com/rajanrx/outbox-md/internal/sse"
	"github.com/rajanrx/outbox-md/internal/store"
)

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

	mux.HandleFunc("GET /api/docs", func(w http.ResponseWriter, _ *http.Request) {
		docs, err := st.ListDocuments()
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

	return mux
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
