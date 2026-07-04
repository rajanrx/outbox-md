package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/rajanrx/outbox-md/internal/service"
	"github.com/rajanrx/outbox-md/internal/store"
)

// registerRAGChat mounts the Ask-the-KB chat endpoints under /api/rag/. Sessions
// are project-scoped; the answer path (POST …/ask) runs AskKB (retrieve → project
// LLM → grounded answer + sources). Human-only/local like the rest of the API — the
// CSRF/Origin guard wrapping the mux covers the state-changing routes.
func registerRAGChat(mux *http.ServeMux, svc *service.Service, st *store.Store) {
	// GET /api/rag/sessions?project=<name> — the project's chat sessions, newest first.
	mux.HandleFunc("GET /api/rag/sessions", func(w http.ResponseWriter, r *http.Request) {
		sessions, err := st.ListSessions(r.URL.Query().Get("project"))
		writeJSON(w, sessions, err)
	})

	// POST /api/rag/sessions {project, title?} — open a new chat session. A blank
	// title defaults to "New chat".
	mux.HandleFunc("POST /api/rag/sessions", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Project string `json:"project"`
			Title   string `json:"title"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSONError(w, http.StatusBadRequest, err)
			return
		}
		title := strings.TrimSpace(in.Title)
		if title == "" {
			title = "New chat"
		}
		sess, err := st.CreateSession(in.Project, title)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSONStatus(w, http.StatusCreated, sess)
	})

	// PATCH /api/rag/sessions/{id} {title} — rename a session. 404 for an unknown id.
	mux.HandleFunc("PATCH /api/rag/sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Title string `json:"title"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSONError(w, http.StatusBadRequest, err)
			return
		}
		if err := st.RenameSession(r.PathValue("id"), strings.TrimSpace(in.Title)); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeJSONError(w, http.StatusNotFound, err)
				return
			}
			writeJSONError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, map[string]any{"ok": true}, nil)
	})

	// DELETE /api/rag/sessions/{id} — delete a session and its messages (cascade).
	mux.HandleFunc("DELETE /api/rag/sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := st.DeleteSession(r.PathValue("id")); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, map[string]any{"ok": true}, nil)
	})

	// GET /api/rag/sessions/{id}/messages — the session's turns in order.
	mux.HandleFunc("GET /api/rag/sessions/{id}/messages", func(w http.ResponseWriter, r *http.Request) {
		msgs, err := st.ListMessages(r.PathValue("id"))
		writeJSON(w, msgs, err)
	})

	// POST /api/rag/sessions/{id}/ask {question} — run AskKB and return the persisted
	// assistant message {content, sources, …}. A missing question is 400; an unwired
	// KB is 503; a spawn/search failure is 502 (the model/retrieval is the upstream).
	mux.HandleFunc("POST /api/rag/sessions/{id}/ask", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Question string `json:"question"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSONError(w, http.StatusBadRequest, err)
			return
		}
		msg, err := svc.AskKB("", r.PathValue("id"), in.Question)
		if err != nil {
			writeJSONError(w, statusForAskErr(err), err)
			return
		}
		writeJSON(w, msg, nil)
	})
}

// statusForAskErr maps an AskKB error to an HTTP status: an unwired KB is 503, an
// unknown session is 404, an empty question is 400, and an upstream search/LLM
// failure is 502 (bad gateway — the model or retrieval, not this server).
func statusForAskErr(err error) int {
	switch {
	case errors.Is(err, service.ErrKBNotWired):
		return http.StatusServiceUnavailable
	case errors.Is(err, sql.ErrNoRows):
		return http.StatusNotFound
	case strings.Contains(err.Error(), "question is required"):
		return http.StatusBadRequest
	default:
		return http.StatusBadGateway
	}
}
