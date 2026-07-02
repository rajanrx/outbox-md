package registry

import (
	"errors"
	"path/filepath"
	"testing"
)

// TestAddRejectsInjectionModel: a member/chair model that isn't a single safe token
// is rejected at Add — so it can never be spliced into a spawn command as extra argv.
func TestAddRejectsInjectionModel(t *testing.T) {
	file := filepath.Join(t.TempDir(), "projects.json")
	root := t.TempDir()
	if _, err := Add(file, root, []string{"."}, []Member{{Agent: "claude", Model: "o3 --cd /tmp"}}, Member{}); !errors.Is(err, ErrBadModel) {
		t.Fatalf("Add with an injection model err = %v, want ErrBadModel", err)
	}
	// A clean model is accepted.
	if _, err := Add(file, root, []string{"."}, []Member{{Agent: "claude", Model: "opus"}}, Member{}); err != nil {
		t.Fatalf("Add with a clean model should succeed: %v", err)
	}
}

// TestValidateDocsRejectsTraversalBeforeStat: a "../escape" docs entry is rejected
// lexically — before any os.Stat — so the stat never probes a request-controlled
// path outside the project root (CodeQL go/path-injection).
func TestValidateDocsRejectsTraversalBeforeStat(t *testing.T) {
	file := filepath.Join(t.TempDir(), "projects.json")
	root := t.TempDir()
	for _, bad := range []string{"../escape", "../../etc", "a/../../b"} {
		if _, err := Add(file, root, []string{bad}, nil, Member{}); !errors.Is(err, ErrBadDocs) {
			t.Fatalf("Add with docs %q err = %v, want ErrBadDocs", bad, err)
		}
	}
}
