package store

import (
	"database/sql"
	"errors"

	"github.com/rajanrx/outbox-md/internal/domain"
)

// CreateDocument creates a document in the single-folder (empty) project. It is
// kept for backward compatibility with existing callers; new project-aware code
// uses CreateDocumentInProject.
func (s *Store) CreateDocument(path, content, createdBy string) (domain.Document, domain.Version, error) {
	return s.CreateDocumentInProject("", path, content, createdBy)
}

// CreateDocumentInProject creates a document keyed by (project, path). Passing an
// empty project is the single-folder mode.
func (s *Store) CreateDocumentInProject(project, path, content, createdBy string) (domain.Document, domain.Version, error) {
	docID := domain.NewID()
	verID := domain.NewID()
	tx, err := s.DB.Begin()
	if err != nil {
		return domain.Document{}, domain.Version{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO documents(id, project, path, current_version_id) VALUES(?,?,?,?)`,
		docID, project, path, verID); err != nil {
		return domain.Document{}, domain.Version{}, err
	}
	if _, err := tx.Exec(`INSERT INTO versions(id, doc_id, ordinal, content, created_by) VALUES(?,?,?,?,?)`,
		verID, docID, 1, content, createdBy); err != nil {
		return domain.Document{}, domain.Version{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Document{}, domain.Version{}, err
	}
	doc := domain.Document{ID: docID, Project: project, Path: path, CurrentVersionID: verID, Status: domain.DocDraft}
	ver := domain.Version{ID: verID, DocID: docID, Ordinal: 1, Content: content, CreatedBy: createdBy}
	return doc, ver, nil
}

func (s *Store) GetDocument(id string) (domain.Document, error) {
	var d domain.Document
	var cur *string
	err := s.DB.QueryRow(`SELECT id, project, path, current_version_id, status, approved_version_id FROM documents WHERE id=?`, id).
		Scan(&d.ID, &d.Project, &d.Path, &cur, &d.Status, &d.ApprovedVersionID)
	if cur != nil {
		d.CurrentVersionID = *cur
	}
	return d, err
}

// GetDocumentByPath looks up a document by its logical key (project, path). Pass
// an empty project for the single-folder mode.
func (s *Store) GetDocumentByPath(project, path string) (domain.Document, bool, error) {
	var d domain.Document
	var cur *string
	err := s.DB.QueryRow(`SELECT id, project, path, current_version_id, status, approved_version_id FROM documents WHERE project=? AND path=?`, project, path).
		Scan(&d.ID, &d.Project, &d.Path, &cur, &d.Status, &d.ApprovedVersionID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Document{}, false, nil
	}
	if cur != nil {
		d.CurrentVersionID = *cur
	}
	return d, err == nil, err
}

func (s *Store) ListDocuments() ([]domain.Document, error) {
	rows, err := s.DB.Query(`SELECT id, project, path, COALESCE(current_version_id,''), status, approved_version_id FROM documents ORDER BY project, path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Document{}
	for rows.Next() {
		var d domain.Document
		if err := rows.Scan(&d.ID, &d.Project, &d.Path, &d.CurrentVersionID, &d.Status, &d.ApprovedVersionID); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ListProjects returns the distinct non-empty project names present in the
// document store, in name order. The single-folder mode's empty project is
// excluded — it is not a named project.
func (s *Store) ListProjects() ([]string, error) {
	rows, err := s.DB.Query(`SELECT DISTINCT project FROM documents WHERE project <> '' ORDER BY project`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) GetVersion(id string) (domain.Version, error) {
	var v domain.Version
	err := s.DB.QueryRow(
		`SELECT id, doc_id, ordinal, content, created_by FROM versions WHERE id=?`, id).
		Scan(&v.ID, &v.DocID, &v.Ordinal, &v.Content, &v.CreatedBy)
	return v, err
}

// ErrVersionConflict means the document's current version changed between the
// caller's staleness check and the transaction — another accept won the race.
var ErrVersionConflict = errors.New("version conflict: document advanced concurrently")

// AddVersionTx records a new version and advances the current pointer inside a
// single transaction, but only if the current pointer is still expectedCurrent
// (a compare-and-swap). This serializes concurrent accepts: two requests that
// both passed a staleness check against the same version cannot both commit —
// the second sees RowsAffected == 0 and gets ErrVersionConflict.
//
// The write callback (e.g. the file write) runs after the CAS succeeds and
// before commit, so the file is only written by the winning accept and the
// database is never left pointing at content the write rejected. Callers should
// still compensate the external side effect when write ran, since a commit
// failure after a successful write is the one window this cannot close alone.
//
// The CAS is also guarded on status='draft'. This is the draft-accept path only
// (governed accepts use AddGovernedVersionTx); guarding on draft closes the
// reverse half of the Approve TOCTOU: if a concurrent Approve pins the baseline
// (flipping status to approved) after this accept read the doc as draft but
// before its CAS, the guard now fails — RowsAffected==0 → ErrVersionConflict —
// so a draft accept can never advance current (and write disk) past a freshly
// pinned approved baseline. The caller requeues, and the agent re-proposes
// against the approved head, which then takes the governed path.
func (s *Store) AddVersionTx(docID, expectedCurrent, content, createdBy string, write func(domain.Version) error) (domain.Version, error) {
	verID := domain.NewID()
	tx, err := s.DB.Begin()
	if err != nil {
		return domain.Version{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// Compare-and-swap the current pointer; advance only if it is unchanged AND
	// the doc is still a draft (a concurrent Approve must not be raced past).
	res, err := tx.Exec(`UPDATE documents SET current_version_id=? WHERE id=? AND current_version_id=? AND status=?`,
		verID, docID, expectedCurrent, domain.DocDraft)
	if err != nil {
		return domain.Version{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return domain.Version{}, err
	}
	if n != 1 {
		return domain.Version{}, ErrVersionConflict
	}

	var maxOrd int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(ordinal),0) FROM versions WHERE doc_id=?`, docID).
		Scan(&maxOrd); err != nil {
		return domain.Version{}, err
	}
	ord := maxOrd + 1
	if _, err := tx.Exec(`INSERT INTO versions(id, doc_id, ordinal, content, created_by) VALUES(?,?,?,?,?)`,
		verID, docID, ord, content, createdBy); err != nil {
		return domain.Version{}, err
	}
	v := domain.Version{ID: verID, DocID: docID, Ordinal: ord, Content: content, CreatedBy: createdBy}
	if write != nil {
		if err := write(v); err != nil {
			return domain.Version{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return domain.Version{}, err
	}
	committed = true
	return v, nil
}

// AddGovernedVersionTx records a new version ahead of the approved baseline for
// an approved/amending document and flips status to amending — all inside one
// transaction, guarded by a compare-and-swap on the current pointer. It mirrors
// AddVersionTx's CAS but never writes disk (governed accepts accumulate ahead of
// the baseline) and never touches approved_version_id: the baseline pointer is
// owned solely by Reapprove, so a stale governed accept can no longer regress it.
//
// The status flip is in the SAME tx as the version advance, so the "amending"
// marker and the new head commit or roll back together. If another writer moved
// the current pointer first, the CAS fails and we return ErrVersionConflict so
// the caller can re-queue exactly like a draft accept that lost the race.
func (s *Store) AddGovernedVersionTx(docID, expectedCurrent, content, createdBy string) (domain.Version, error) {
	verID := domain.NewID()
	tx, err := s.DB.Begin()
	if err != nil {
		return domain.Version{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// Compare-and-swap the current pointer; advance only if it is unchanged.
	res, err := tx.Exec(`UPDATE documents SET current_version_id=? WHERE id=? AND current_version_id=?`,
		verID, docID, expectedCurrent)
	if err != nil {
		return domain.Version{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return domain.Version{}, err
	}
	if n != 1 {
		return domain.Version{}, ErrVersionConflict
	}

	var maxOrd int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(ordinal),0) FROM versions WHERE doc_id=?`, docID).
		Scan(&maxOrd); err != nil {
		return domain.Version{}, err
	}
	ord := maxOrd + 1
	if _, err := tx.Exec(`INSERT INTO versions(id, doc_id, ordinal, content, created_by) VALUES(?,?,?,?,?)`,
		verID, docID, ord, content, createdBy); err != nil {
		return domain.Version{}, err
	}
	// Mark the document amending — status only; never rewrite approved_version_id.
	if _, err := tx.Exec(`UPDATE documents SET status=? WHERE id=?`, domain.DocAmending, docID); err != nil {
		return domain.Version{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Version{}, err
	}
	committed = true
	return domain.Version{ID: verID, DocID: docID, Ordinal: ord, Content: content, CreatedBy: createdBy}, nil
}

// ReapproveTx advances the approved baseline to expectedCurrent and writes that
// content to disk inside one transaction, guarded by a compare-and-swap on the
// current pointer. The baseline only moves if the working head is still
// expectedCurrent; if a concurrent accept advanced current under us, the CAS
// affects 0 rows and we return ErrVersionConflict without writing disk — so a
// re-approval can never pin a baseline the human did not actually review.
//
// The write callback (the disk write) runs after the CAS succeeds and before
// commit, mirroring AddVersionTx. As with AddVersionTx, if write ran but the
// commit fails the caller must compensate the on-disk file back to the prior
// baseline content — that post-write-pre-commit window is the one case this
// cannot close alone. On the ErrVersionConflict path write never runs, so no
// compensation is needed there.
func (s *Store) ReapproveTx(docID, expectedCurrent, content string, write func() error) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// CAS the baseline + status, guarded on the current pointer being unchanged.
	res, err := tx.Exec(`UPDATE documents SET approved_version_id=?, status=? WHERE id=? AND current_version_id=?`,
		expectedCurrent, domain.DocApproved, docID, expectedCurrent)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrVersionConflict
	}
	if write != nil {
		if err := write(); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *Store) AddVersion(docID, content, createdBy string) (domain.Version, error) {
	verID := domain.NewID()
	tx, err := s.DB.Begin()
	if err != nil {
		return domain.Version{}, err
	}
	defer tx.Rollback()
	var maxOrd int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(ordinal),0) FROM versions WHERE doc_id=?`, docID).
		Scan(&maxOrd); err != nil {
		return domain.Version{}, err
	}
	ord := maxOrd + 1
	if _, err := tx.Exec(`INSERT INTO versions(id, doc_id, ordinal, content, created_by) VALUES(?,?,?,?,?)`,
		verID, docID, ord, content, createdBy); err != nil {
		return domain.Version{}, err
	}
	if _, err := tx.Exec(`UPDATE documents SET current_version_id=? WHERE id=?`, verID, docID); err != nil {
		return domain.Version{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Version{}, err
	}
	return domain.Version{ID: verID, DocID: docID, Ordinal: ord, Content: content, CreatedBy: createdBy}, nil
}

// SetDocumentApproval pins the approved baseline and sets the lifecycle status.
// Pass the same approvedVersionID to keep the baseline (e.g. status -> amending).
func (s *Store) SetDocumentApproval(docID, approvedVersionID string, status domain.DocumentStatus) error {
	_, err := s.DB.Exec(`UPDATE documents SET approved_version_id=?, status=? WHERE id=?`,
		approvedVersionID, status, docID)
	return err
}

// SetDocumentApprovalIfCurrent pins the approved baseline and sets the lifecycle
// status, but only if the current pointer is still expectedCurrent (a
// compare-and-swap). This closes the Approve TOCTOU: a concurrent accept that
// advanced the current pointer between Approve's read and its pin makes the
// guard match 0 rows, so we return ErrVersionConflict instead of pinning a stale
// baseline behind the new current. A single conditional UPDATE — no transaction
// needed; with SetMaxOpenConns(1) a loser deterministically sees committed state
// and gets a clean conflict rather than SQLITE_BUSY.
func (s *Store) SetDocumentApprovalIfCurrent(docID, expectedCurrent, approvedVersionID string, status domain.DocumentStatus) error {
	res, err := s.DB.Exec(`UPDATE documents SET approved_version_id=?, status=? WHERE id=? AND current_version_id=?`,
		approvedVersionID, status, docID, expectedCurrent)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrVersionConflict
	}
	return nil
}
