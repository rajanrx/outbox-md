package api

import (
	"encoding/json"
	"net/http"
	"os"

	"github.com/rajanrx/outbox-md/internal/domain"
	"github.com/rajanrx/outbox-md/internal/service"
	"github.com/rajanrx/outbox-md/internal/store"
)

func NewAPI(svc *service.Service, st *store.Store) http.Handler {
	mux := http.NewServeMux()

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
		writeJSON(w, map[string]any{
			"document": doc, "content": ver.Content, "comments": comments,
		}, nil)
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
