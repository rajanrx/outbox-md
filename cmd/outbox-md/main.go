package main

import (
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rajanrx/outbox-md/internal/api"
	"github.com/rajanrx/outbox-md/internal/config"
	"github.com/rajanrx/outbox-md/internal/mcp"
	"github.com/rajanrx/outbox-md/internal/service"
	"github.com/rajanrx/outbox-md/internal/store"
	"github.com/rajanrx/outbox-md/web"
)

// newMux is the minimal handler used by the health-check test.
func newMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}

// safeJoin resolves path under dir and refuses any result that escapes dir
// (defense-in-depth against path traversal on the file-write path).
func safeJoin(dir, path string) (string, error) {
	target := filepath.Join(dir, path)
	rel, err := filepath.Rel(dir, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("refusing to write outside managed dir: %q", path)
	}
	return target, nil
}

// atomicWrite writes content to a temp file in the same directory and renames
// it into place, so a failed or partial write never corrupts the target file.
// It preserves the target's permission bits, and best-effort its ownership, so
// the rename does not silently change mode (CreateTemp defaults to 0600) or
// leave a root-owned file on a Docker bind mount.
func atomicWrite(target, content string) error {
	mode := os.FileMode(0o644)
	uid, gid := -1, -1
	if fi, err := os.Stat(target); err == nil {
		mode = fi.Mode().Perm()
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			uid, gid = int(st.Uid), int(st.Gid)
		}
	}

	tmp, err := os.CreateTemp(filepath.Dir(target), ".outbox-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		cleanup()
		return err
	}
	if uid >= 0 {
		// Best-effort: only succeeds when permitted (e.g. running as root in
		// Docker). A failure here must not block the write.
		_ = os.Chown(tmpName, uid, gid)
	}
	if err := os.Rename(tmpName, target); err != nil {
		cleanup()
		return err
	}
	return nil
}

// ensureDataDir verifies the data path is a directory (creating it if absent),
// and fails with a clear message if it points at a file — the most common
// mistake (mounting a single .md instead of a folder).
func ensureDataDir(dir string) error {
	fi, err := os.Stat(dir)
	if err == nil {
		if !fi.IsDir() {
			return fmt.Errorf("data path %q is a file, not a directory — mount a folder of .md files "+
				"(e.g. -v \"$PWD/specs:/data\"), not a single file", dir)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	return os.MkdirAll(dir, 0o755)
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func importMarkdown(st *store.Store, dir string) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".outbox" || (strings.HasPrefix(d.Name(), ".") && p != dir) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		if _, ok, _ := st.GetDocumentByPath(rel); ok {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		_, _, err = st.CreateDocument(rel, string(b), "import")
		return err
	})
}

func main() {
	dir := getenv("OUTBOX_DIR", "/data")
	addr := getenv("OUTBOX_ADDR", ":8181")

	if err := ensureDataDir(dir); err != nil {
		log.Fatal(err)
	}
	dbDir := filepath.Join(dir, ".outbox")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		log.Fatal(err)
	}
	st, err := store.Open("file:" + filepath.Join(dbDir, "outbox.db"))
	if err != nil {
		log.Fatal(err)
	}
	svc := service.New(st, func(path, content string) error {
		target, err := safeJoin(dir, path)
		if err != nil {
			return err
		}
		return atomicWrite(target, content)
	})
	svc.SetConfig(config.Load(dir))
	if err := importMarkdown(st, dir); err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("/api/", api.NewAPI(svc, st))

	// MCP over Streamable HTTP at /mcp — any agent connects here.
	mcpServer := mcp.NewServer(&mcp.Handlers{Svc: svc, St: st})
	mcpHandler := mcpsdk.NewStreamableHTTPHandler(
		func(*http.Request) *mcpsdk.Server { return mcpServer }, nil)
	mux.Handle("/mcp", mcpHandler)
	mux.Handle("/mcp/", mcpHandler)

	sub, _ := fs.Sub(web.Dist, "dist")
	mux.Handle("/", http.FileServer(http.FS(sub)))

	log.Printf("outbox-md on %s serving %s (mcp at /mcp)", addr, dir)
	log.Fatal(http.ListenAndServe(addr, mux))
}
