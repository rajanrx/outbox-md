package store

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/rajanrx/outbox-md/internal/domain"
)

func TestAddVersionTxCAS(t *testing.T) {
	s, _ := Open(":memory:")
	defer s.Close()
	doc, v1, _ := s.CreateDocument("spec.md", "a", "human")

	// Advance v1 -> v2 (CAS against the real current version succeeds).
	v2, err := s.AddVersionTx(doc.ID, v1.ID, "b", "agent", nil)
	if err != nil {
		t.Fatal(err)
	}

	// A second CAS against the now-stale v1 must conflict, not advance.
	if _, err := s.AddVersionTx(doc.ID, v1.ID, "c", "agent", nil); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("expected ErrVersionConflict, got %v", err)
	}
	cur, _ := s.GetDocument(doc.ID)
	if cur.CurrentVersionID != v2.ID {
		t.Fatal("conflicting CAS must not advance the current version")
	}

	// CAS against the real current version (v2) succeeds.
	if _, err := s.AddVersionTx(doc.ID, v2.ID, "c", "agent", nil); err != nil {
		t.Fatal(err)
	}
}

func TestCreateAndVersion(t *testing.T) {
	s, _ := Open(":memory:")
	defer s.Close()

	doc, v1, err := s.CreateDocument("spec.md", "hello", "human")
	if err != nil {
		t.Fatal(err)
	}
	if v1.Ordinal != 1 || v1.Content != "hello" {
		t.Fatalf("v1 = %+v", v1)
	}
	if doc.CurrentVersionID != v1.ID {
		t.Fatal("current version not set to v1")
	}

	v2, err := s.AddVersion(doc.ID, "hello world", "agent")
	if err != nil {
		t.Fatal(err)
	}
	if v2.Ordinal != 2 {
		t.Fatalf("v2 ordinal = %d, want 2", v2.Ordinal)
	}
	got, _ := s.GetDocument(doc.ID)
	if got.CurrentVersionID != v2.ID {
		t.Fatal("current version not advanced to v2")
	}
}

func TestNewDocumentDefaultsToDraft(t *testing.T) {
	s, _ := Open(":memory:")
	defer s.Close()
	doc, _, err := s.CreateDocument("a.md", "hello", "human")
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetDocument(doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.DocDraft {
		t.Errorf("status = %q, want draft", got.Status)
	}
	if got.ApprovedVersionID != "" {
		t.Errorf("approvedVersionId = %q, want empty", got.ApprovedVersionID)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	s, _ := Open(":memory:")
	defer s.Close()
	// migrate already ran inside Open; running it again must be a no-op.
	if err := migrate(s.DB); err != nil {
		t.Fatalf("re-migrate: %v", err)
	}
}

// TestMigrationAddsProjectColumn confirms the documents table has a project
// column (from schema.sql on a fresh DB; migrate() covers legacy DBs).
func TestMigrationAddsProjectColumn(t *testing.T) {
	s, _ := Open(":memory:")
	defer s.Close()
	var project string
	if err := s.DB.QueryRow(`SELECT project FROM documents LIMIT 0`).Scan(&project); err != nil {
		// No rows is fine (LIMIT 0); the point is the column must resolve.
		if err.Error() != "sql: no rows in result set" {
			t.Fatalf("project column not present: %v", err)
		}
	}
}

// TestMigrateAddsProjectToLegacyDB exercises the ADD COLUMN path that a fresh
// schema.sql database never triggers: it builds a documents table in the OLD
// pre-project shape, runs migrate(), and confirms the project column and the
// (project, path) index now exist and round-trip.
func TestMigrateAddsProjectToLegacyDB(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	// Original shape: no project column, standalone UNIQUE(path).
	if _, err := db.Exec(`CREATE TABLE documents (
		id TEXT PRIMARY KEY,
		path TEXT NOT NULL UNIQUE,
		current_version_id TEXT,
		status TEXT NOT NULL DEFAULT 'draft',
		approved_version_id TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO documents(id, path) VALUES('d1','legacy.md')`); err != nil {
		t.Fatal(err)
	}
	// migrate() also ALTERs comments; a legacy DB has that table, so give the
	// fixture a minimal one for the migration to operate on.
	if _, err := db.Exec(`CREATE TABLE comments (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(db); err != nil {
		t.Fatalf("migrate on legacy DB: %v", err)
	}
	// The project column now resolves and legacy rows default to ''.
	var project string
	if err := db.QueryRow(`SELECT project FROM documents WHERE id='d1'`).Scan(&project); err != nil {
		t.Fatalf("project column missing after migrate: %v", err)
	}
	if project != "" {
		t.Fatalf("legacy row project = %q, want empty", project)
	}
	// The composite index exists.
	var idx string
	if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='index' AND name='idx_documents_project_path'`).Scan(&idx); err != nil {
		t.Fatalf("(project, path) index missing after migrate: %v", err)
	}
	// A '' -keyed insert still round-trips (single-folder back-compat on a
	// migrated DB): a different path under project '' is accepted.
	if _, err := db.Exec(`INSERT INTO documents(id, project, path) VALUES('d2','','other.md')`); err != nil {
		t.Fatalf("empty-project insert after migrate: %v", err)
	}
}

// TestCreateInProjectRoundTrip verifies a project is persisted and returned by
// CreateDocumentInProject, ListDocuments, GetDocument and GetDocumentByPath.
func TestCreateInProjectRoundTrip(t *testing.T) {
	s, _ := Open(":memory:")
	defer s.Close()
	doc, _, err := s.CreateDocumentInProject("alpha", "spec.md", "hi", "human")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Project != "alpha" {
		t.Fatalf("create project = %q, want alpha", doc.Project)
	}
	got, err := s.GetDocument(doc.ID)
	if err != nil || got.Project != "alpha" {
		t.Fatalf("GetDocument project = %q err=%v, want alpha", got.Project, err)
	}
	byPath, ok, err := s.GetDocumentByPath("alpha", "spec.md")
	if err != nil || !ok || byPath.ID != doc.ID {
		t.Fatalf("GetDocumentByPath(alpha, spec.md) = %+v ok=%v err=%v", byPath, ok, err)
	}
	// A different project with the SAME path must not collide (distinct docs).
	if _, _, err := s.CreateDocumentInProject("beta", "spec.md", "hi", "human"); err != nil {
		t.Fatalf("same path in a different project must be allowed: %v", err)
	}
	docs, _ := s.ListDocuments()
	if len(docs) != 2 {
		t.Fatalf("ListDocuments = %d, want 2 (alpha+beta spec.md)", len(docs))
	}
	projects, _ := s.ListProjects()
	if len(projects) != 2 || projects[0] != "alpha" || projects[1] != "beta" {
		t.Fatalf("ListProjects = %v, want [alpha beta]", projects)
	}
}

// TestProjectPathUniqueness verifies (project, path) is unique: the same path in
// the same project is rejected, and the empty-project back-compat path is
// unaffected.
func TestProjectPathUniqueness(t *testing.T) {
	s, _ := Open(":memory:")
	defer s.Close()
	if _, _, err := s.CreateDocumentInProject("alpha", "spec.md", "a", "human"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CreateDocumentInProject("alpha", "spec.md", "b", "human"); err == nil {
		t.Fatal("expected uniqueness violation for duplicate (project, path)")
	}
}

// TestEmptyProjectBackCompat verifies CreateDocument (no project) still keys
// under the empty project and is excluded from ListProjects.
func TestEmptyProjectBackCompat(t *testing.T) {
	s, _ := Open(":memory:")
	defer s.Close()
	doc, _, err := s.CreateDocument("spec.md", "hi", "human")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Project != "" {
		t.Fatalf("back-compat project = %q, want empty", doc.Project)
	}
	if _, ok, _ := s.GetDocumentByPath("", "spec.md"); !ok {
		t.Fatal("GetDocumentByPath(\"\", spec.md) should find the doc")
	}
	if projects, _ := s.ListProjects(); len(projects) != 0 {
		t.Fatalf("empty project must be excluded from ListProjects, got %v", projects)
	}
}
