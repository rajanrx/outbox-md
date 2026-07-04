package store

import (
	"database/sql"
	"encoding/json"

	"github.com/rajanrx/outbox-md/internal/domain"
	"github.com/rajanrx/outbox-md/internal/rag"
)

// RagSession is one persistent "Ask the KB" chat conversation, scoped to a single
// project (the empty project is single-folder mode). Title is a short human label;
// CreatedAt is the RFC-ish datetime('now') the session was opened.
type RagSession struct {
	ID        string `json:"id"`
	Project   string `json:"project"`
	Title     string `json:"title"`
	CreatedAt string `json:"createdAt"`
}

// RagMessage is one turn in a chat session: a user question or an assistant
// answer. Sources is the retrieved KB passages the assistant answer was grounded
// in (empty for a user turn); it round-trips through the sources_json TEXT column
// as a JSON array. Placing the type here (not in domain) is deliberate: rag
// imports domain, so domain cannot hold []rag.Result without a cycle — store may
// import rag freely (rag never imports store).
type RagMessage struct {
	ID        string       `json:"id"`
	SessionID string       `json:"sessionId"`
	Role      string       `json:"role"` // "user" | "assistant"
	Content   string       `json:"content"`
	Sources   []rag.Result `json:"sources"`
	CreatedAt string       `json:"createdAt"`
}

// RagRoleUser / RagRoleAssistant are the two message roles.
const (
	RagRoleUser      = "user"
	RagRoleAssistant = "assistant"
)

// CreateSession opens a new chat session for project with the given title (a blank
// title is stored as-is; the caller may supply a default). The id/createdAt are
// generated here and returned on the populated session.
func (s *Store) CreateSession(project, title string) (RagSession, error) {
	sess := RagSession{ID: domain.NewID(), Project: project, Title: title}
	if _, err := s.DB.Exec(
		`INSERT INTO rag_sessions(id, project, title) VALUES(?,?,?)`,
		sess.ID, sess.Project, sess.Title); err != nil {
		return RagSession{}, err
	}
	if err := s.DB.QueryRow(
		`SELECT created_at FROM rag_sessions WHERE id=?`, sess.ID).Scan(&sess.CreatedAt); err != nil {
		return RagSession{}, err
	}
	return sess, nil
}

// ListSessions returns project's chat sessions, most recent first (a fresh chat
// appears at the top of the sidebar). It returns a non-nil (possibly empty) slice.
func (s *Store) ListSessions(project string) ([]RagSession, error) {
	rows, err := s.DB.Query(
		`SELECT id, project, title, created_at FROM rag_sessions
		 WHERE project=? ORDER BY created_at DESC, rowid DESC`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RagSession{}
	for rows.Next() {
		var r RagSession
		if err := rows.Scan(&r.ID, &r.Project, &r.Title, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetSession loads one session by id.
func (s *Store) GetSession(id string) (RagSession, error) {
	var r RagSession
	err := s.DB.QueryRow(
		`SELECT id, project, title, created_at FROM rag_sessions WHERE id=?`, id).
		Scan(&r.ID, &r.Project, &r.Title, &r.CreatedAt)
	return r, err
}

// RenameSession updates a session's title. An unknown id returns sql.ErrNoRows so
// the API can map it to 404.
func (s *Store) RenameSession(id, title string) error {
	res, err := s.DB.Exec(`UPDATE rag_sessions SET title=? WHERE id=?`, title, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// DeleteSession removes a session and ALL of its messages. Foreign keys are not
// enforced (no PRAGMA foreign_keys=ON in Open), so the cascade is done explicitly
// here — messages first, then the session — inside one transaction so a session is
// never left with orphaned messages nor messages without their session.
func (s *Store) DeleteSession(id string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM rag_messages WHERE session_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM rag_sessions WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// AppendMessage stores one message turn on a session, marshalling its sources to
// the sources_json column. A nil/empty Sources round-trips as "[]" so ListMessages
// always returns a non-nil slice. The id/createdAt are generated here and returned
// on the populated message.
func (s *Store) AppendMessage(m RagMessage) (RagMessage, error) {
	if m.ID == "" {
		m.ID = domain.NewID()
	}
	if m.Sources == nil {
		m.Sources = []rag.Result{}
	}
	raw, err := json.Marshal(m.Sources)
	if err != nil {
		return RagMessage{}, err
	}
	if _, err := s.DB.Exec(
		`INSERT INTO rag_messages(id, session_id, role, content, sources_json)
		 VALUES(?,?,?,?,?)`,
		m.ID, m.SessionID, m.Role, m.Content, string(raw)); err != nil {
		return RagMessage{}, err
	}
	if err := s.DB.QueryRow(
		`SELECT created_at FROM rag_messages WHERE id=?`, m.ID).Scan(&m.CreatedAt); err != nil {
		return RagMessage{}, err
	}
	return m, nil
}

// AppendExchange writes a user turn and the assistant turn it produced in ONE
// transaction, so a failed assistant insert never leaves a dangling user message —
// AskKB persists a full exchange or nothing. Returns the stored assistant message.
func (s *Store) AppendExchange(user, assistant RagMessage) (RagMessage, error) {
	if assistant.ID == "" {
		assistant.ID = domain.NewID()
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return RagMessage{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := insertMessageTx(tx, user); err != nil {
		return RagMessage{}, err
	}
	if err := insertMessageTx(tx, assistant); err != nil {
		return RagMessage{}, err
	}
	if err := tx.Commit(); err != nil {
		return RagMessage{}, err
	}
	if err := s.DB.QueryRow(
		`SELECT created_at FROM rag_messages WHERE id=?`, assistant.ID).Scan(&assistant.CreatedAt); err != nil {
		return RagMessage{}, err
	}
	return assistant, nil
}

// insertMessageTx inserts one rag message within tx (id + sources defaulted).
func insertMessageTx(tx *sql.Tx, m RagMessage) error {
	if m.ID == "" {
		m.ID = domain.NewID()
	}
	if m.Sources == nil {
		m.Sources = []rag.Result{}
	}
	raw, err := json.Marshal(m.Sources)
	if err != nil {
		return err
	}
	_, err = tx.Exec(
		`INSERT INTO rag_messages(id, session_id, role, content, sources_json)
		 VALUES(?,?,?,?,?)`,
		m.ID, m.SessionID, m.Role, m.Content, string(raw))
	return err
}

// ListMessages returns a session's messages in turn order (insertion order via
// created_at then rowid — a user+assistant pair written in the same second still
// renders in the right order). It returns a non-nil (possibly empty) slice; each
// message's sources are decoded from its sources_json.
func (s *Store) ListMessages(sessionID string) ([]RagMessage, error) {
	rows, err := s.DB.Query(
		`SELECT id, session_id, role, content, sources_json, created_at
		 FROM rag_messages WHERE session_id=? ORDER BY created_at, rowid`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RagMessage{}
	for rows.Next() {
		var m RagMessage
		var raw string
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Role, &m.Content, &raw, &m.CreatedAt); err != nil {
			return nil, err
		}
		if raw == "" {
			m.Sources = []rag.Result{}
		} else if err := json.Unmarshal([]byte(raw), &m.Sources); err != nil {
			return nil, err
		}
		if m.Sources == nil {
			m.Sources = []rag.Result{}
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
