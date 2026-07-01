package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/rajanrx/outbox-md/internal/config"
)

func withSeam(t *testing.T, seam *string, val string) {
	t.Helper()
	old := *seam
	*seam = val
	t.Cleanup(func() { *seam = old })
}

func TestLatestReleaseParsesTag(t *testing.T) {
	// v<version> is the current tag form; outbox-md-v<version> is the legacy form
	// (kept working so a pre-cutover release still parses).
	for tag, want := range map[string]string{"v0.8.0": "0.8.0", "outbox-md-v0.7.0": "0.7.0"} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprintf(w, `{"tag_name":%q}`, tag)
		}))
		withSeam(t, &latestReleaseAPI, srv.URL)
		v, err := latestRelease()
		srv.Close()
		if err != nil || v != want {
			t.Fatalf("latestRelease() for tag %q = %q, %v; want %q, nil", tag, v, err, want)
		}
	}
}

func TestLatestReleaseServerErrorIsNoUpdate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	withSeam(t, &latestReleaseAPI, srv.URL)

	if _, err := latestRelease(); err == nil {
		t.Fatal("latestRelease() on 500 must error so the caller treats it as no-update")
	}
}

// releaseServer serves the asset + checksums.txt for the current GOOS/GOARCH.
func releaseServer(t *testing.T, bin []byte, sum string) *httptest.Server {
	t.Helper()
	asset := fmt.Sprintf("outbox_%s_%s", runtime.GOOS, runtime.GOARCH)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, asset):
			_, _ = w.Write(bin)
		case strings.HasSuffix(r.URL.Path, "checksums.txt"):
			_, _ = io.WriteString(w, sum+"  "+asset+"\n")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func noLeftoverTemp(t *testing.T, dir string) {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".outbox-update-") {
			t.Fatalf("leftover temp file %s", e.Name())
		}
	}
}

func TestSelfReplaceToSwapsOnValidChecksum(t *testing.T) {
	newBin := []byte("NEW-BINARY-BYTES")
	sum := sha256.Sum256(newBin)
	srv := releaseServer(t, newBin, hex.EncodeToString(sum[:]))
	defer srv.Close()
	withSeam(t, &releaseDownloadBase, srv.URL)

	dir := t.TempDir()
	exe := filepath.Join(dir, "outbox")
	if err := os.WriteFile(exe, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := selfReplaceTo(exe, "0.7.0"); err != nil {
		t.Fatalf("selfReplaceTo: %v", err)
	}
	if got, _ := os.ReadFile(exe); string(got) != string(newBin) {
		t.Fatalf("exe = %q, want the new binary", got)
	}
	noLeftoverTemp(t, dir)
}

func TestSelfReplaceToLeavesBinaryOnChecksumMismatch(t *testing.T) {
	// Server hands out bytes that do NOT match the advertised sum (tamper).
	srv := releaseServer(t, []byte("TAMPERED"), "deadbeef")
	defer srv.Close()
	withSeam(t, &releaseDownloadBase, srv.URL)

	dir := t.TempDir()
	exe := filepath.Join(dir, "outbox")
	if err := os.WriteFile(exe, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := selfReplaceTo(exe, "0.7.0"); err == nil {
		t.Fatal("checksum mismatch must return an error")
	}
	if got, _ := os.ReadFile(exe); string(got) != "OLD" {
		t.Fatalf("exe changed to %q on mismatch — must stay OLD (no rename)", got)
	}
	noLeftoverTemp(t, dir) // and no half-written binary lands on PATH
}

// The relayed P2: auto_update:false must NOT make the daily network call.
func TestMaybeAutoUpdateOptOutMakesNoNetworkCall(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		fmt.Fprint(w, `{"tag_name":"outbox-md-v9.9.9"}`)
	}))
	defer srv.Close()
	withSeam(t, &latestReleaseAPI, srv.URL)
	// Non-dev version so the dev short-circuit doesn't mask the opt-out path.
	withSeam(t, &version, "0.1.0")

	maybeAutoUpdate(config.Config{AutoUpdate: false}, io.Discard)
	if hits != 0 {
		t.Fatalf("auto_update:false made %d network call(s) on startup, want 0", hits)
	}
}
