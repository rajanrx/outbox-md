package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/rajanrx/outbox-md/internal/config"
)

// These mirror the release conventions from the binaries job (see
// .github/workflows/release-please.yml): binaries are named
// outbox_<goos>_<goarch> with a single checksums.txt, attached to a GitHub
// Release tagged outbox-md-v<version>. They are vars (not consts) so tests could
// point them at a local server if ever needed — the unit tests here never touch
// the network.
var (
	// latestReleaseAPI returns the newest release as JSON with a .tag_name.
	latestReleaseAPI = "https://api.github.com/repos/rajanrx/outbox-md/releases/latest"
	// releaseDownloadBase is the prefix for a tagged release's assets:
	// <base>/<tag>/<asset>.
	releaseDownloadBase = "https://github.com/rajanrx/outbox-md/releases/download"
	// tagPrefix is stripped from a tag to get a bare X.Y.Z version, and re-added
	// when building a download URL.
	tagPrefix = "outbox-md-v"
)

// updateCheckInterval throttles the startup update check to at most once per day.
const updateCheckInterval = 24 * time.Hour

// installKind describes how this binary was installed, which decides whether we
// may self-replace it or must defer to another update channel.
type installKind string

const (
	kindStandalone installKind = "standalone" // a plain binary on disk — self-replaceable
	kindHomebrew   installKind = "homebrew"   // managed by Homebrew — use `brew upgrade`
	kindDocker     installKind = "docker"     // running in a container — update the image
	kindUnknown    installKind = "unknown"    // can't tell / can't self-update
)

// semverNewer reports whether latest is a strictly newer X.Y.Z than current.
// A "dev" current is never considered older (local builds never self-update),
// and any unparseable version compares as "not newer" so a bad tag can never
// trigger an update.
func semverNewer(current, latest string) bool {
	if current == "dev" {
		return false
	}
	cur, ok1 := parseSemver(current)
	lat, ok2 := parseSemver(latest)
	if !ok1 || !ok2 {
		return false
	}
	for i := 0; i < 3; i++ {
		if lat[i] != cur[i] {
			return lat[i] > cur[i]
		}
	}
	return false
}

// parseSemver parses "X.Y.Z" (with an optional leading v and an ignored
// -prerelease/+build suffix) into three ints. It compares numerically, so
// 0.10.0 > 0.9.0 (a lexical compare would get this wrong).
func parseSemver(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	// Drop any -prerelease or +build metadata; we only compare the core triple.
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// classifyPath decides the install kind from the executable path and whether we
// are in a container. It is pure (no filesystem access) so the decision logic is
// unit-testable without depending on where the test binary actually lives.
func classifyPath(exePath string, dockerEnv bool) installKind {
	if dockerEnv {
		return kindDocker
	}
	if strings.Contains(exePath, "/Cellar/") ||
		strings.HasPrefix(exePath, "/opt/homebrew/") ||
		strings.HasPrefix(exePath, "/usr/local/Cellar/") {
		return kindHomebrew
	}
	return kindStandalone
}

// inDocker reports whether we appear to be running inside a container: the
// Docker-created /.dockerenv marker, or OUTBOX_CONTAINER=1 which the image sets
// (see Dockerfile) so a distroless container with no /.dockerenv is still
// detectable.
func inDocker() bool {
	if os.Getenv("OUTBOX_CONTAINER") == "1" {
		return true
	}
	_, err := os.Stat("/.dockerenv")
	return err == nil
}

// installKindOf resolves the running binary's install kind. It follows any
// symlink (Homebrew installs a symlink into a Cellar path) and requires a
// regular file for the standalone case — anything else is "unknown" and won't
// self-replace. Writability is not probed here: selfReplace's atomic rename
// surfaces a permission error if the binary isn't ours to replace.
func installKindOf() installKind {
	exe, err := os.Executable()
	if err != nil {
		return kindUnknown
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	kind := classifyPath(exe, inDocker())
	if kind == kindStandalone {
		if fi, err := os.Stat(exe); err != nil || !fi.Mode().IsRegular() {
			return kindUnknown
		}
	}
	return kind
}

// latestRelease fetches the newest release's version (tag_name minus the
// outbox-md-v prefix) from the GitHub API. Any error — offline, rate-limited,
// unexpected tag — is returned so the caller treats it as "no update available"
// and moves on. A short timeout guarantees this can never wedge startup.
func latestRelease() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, latestReleaseAPI, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("release check: unexpected status %s", resp.Status)
	}
	var body struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return "", err
	}
	ver := strings.TrimPrefix(body.TagName, tagPrefix)
	if ver == "" || ver == body.TagName {
		return "", fmt.Errorf("release check: unexpected tag %q", body.TagName)
	}
	return ver, nil
}

// checksumFor returns the sha256 hex for asset from a sha256sum-style
// checksums.txt ("<hex>  <name>" per line). It matches by whitespace-split
// fields (GNU sha256sum uses two spaces for text and " *" for binary mode) so
// the parse is robust to either spacing.
func checksumFor(sums, asset string) (string, bool) {
	for _, line := range strings.Split(sums, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		// The name may carry a leading '*' (binary mode); strip it before matching.
		name := strings.TrimPrefix(fields[1], "*")
		if name == asset {
			return fields[0], true
		}
	}
	return "", false
}

// download GETs url with a timeout and returns its body, capped so a wrong URL
// can't stream unbounded data. Used for the release binary + its checksums.
func download(url string, limit int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: unexpected status %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// selfReplace downloads the release binary for this OS/arch at the given
// version, verifies its sha256 against the release's checksums.txt, and swaps it
// over the running executable via a temp file + atomic rename in the same
// directory. On any failure it returns an error and leaves the current binary
// untouched — a half-written binary never lands on PATH.
func selfReplace(version string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return selfReplaceTo(exe, version)
}

// selfReplaceTo downloads and sha256-verifies the release asset for version and
// atomically swaps it over exe (temp file + rename in the same dir). Split from
// selfReplace (which resolves the running executable) so the download → verify →
// swap can be exercised against a temp file and a local server in tests.
func selfReplaceTo(exe, version string) error {
	asset := fmt.Sprintf("outbox_%s_%s", runtime.GOOS, runtime.GOARCH)
	base := fmt.Sprintf("%s/%s%s", releaseDownloadBase, tagPrefix, version)

	binData, err := download(base+"/"+asset, 200<<20) // 200 MiB cap — generous for a static Go binary
	if err != nil {
		return fmt.Errorf("download binary: %w", err)
	}
	sumsData, err := download(base+"/checksums.txt", 1<<20)
	if err != nil {
		return fmt.Errorf("download checksums: %w", err)
	}
	want, ok := checksumFor(string(sumsData), asset)
	if !ok {
		return fmt.Errorf("checksums.txt has no entry for %s", asset)
	}
	sum := sha256.Sum256(binData)
	if got := hex.EncodeToString(sum[:]); got != want {
		return fmt.Errorf("checksum mismatch for %s (want %s, got %s)", asset, want, got)
	}

	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".outbox-update-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(binData); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		cleanup()
		return err
	}
	// Atomic same-filesystem swap over the running binary (valid on Unix — the
	// running process keeps its already-open image; the path now points at the
	// new file).
	if err := os.Rename(tmpName, exe); err != nil {
		cleanup()
		return fmt.Errorf("replace %s: %w", exe, err)
	}
	return nil
}

// upgrade is the `outbox upgrade` command: force an update now regardless of the
// auto_update flag. It resolves the latest release; if not newer, reports
// already-up-to-date; if newer, self-replaces for standalone installs or points
// Homebrew/Docker users at the right channel. Returns an error (non-zero exit)
// only when the check or the replacement actually fails.
func upgrade(out io.Writer) error {
	if version == "dev" {
		fmt.Fprintln(out, "dev build — self-update is disabled; install a release binary to enable `outbox upgrade`")
		return nil
	}
	latest, err := latestRelease()
	if err != nil {
		return fmt.Errorf("could not check for updates: %w", err)
	}
	if !semverNewer(version, latest) {
		fmt.Fprintf(out, "already up to date (v%s)\n", version)
		return nil
	}
	switch installKindOf() {
	case kindStandalone:
		if err := selfReplace(latest); err != nil {
			return fmt.Errorf("update failed: %w", err)
		}
		fmt.Fprintf(out, "updated v%s → v%s\n", version, latest)
		return nil
	case kindHomebrew:
		fmt.Fprintf(out, "v%s available — run `brew upgrade outbox-md` to update\n", latest)
		return nil
	case kindDocker:
		fmt.Fprintf(out, "v%s available — update the container image (see Watchtower in the README)\n", latest)
		return nil
	default:
		fmt.Fprintf(out, "v%s available — reinstall from https://github.com/rajanrx/outbox-md to update\n", latest)
		return nil
	}
}

// throttleDue reports whether a check is due: no prior check (zero last) or at
// least interval has elapsed. Pure, so the throttle decision is unit-testable.
func throttleDue(last, now time.Time, interval time.Duration) bool {
	return now.Sub(last) >= interval
}

// throttlePath is the file that records the last update-check time. A failure to
// locate the user cache dir is returned so the caller can skip the throttle
// gracefully (it still runs the check, just without persistence).
func throttlePath() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cache, "outbox", "last-update-check"), nil
}

// readLastCheck reads the persisted last-check time, returning the zero time if
// the file is absent or unparseable (either way, a check is then due).
func readLastCheck(path string) time.Time {
	b, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(string(b)))
	if err != nil {
		return time.Time{}
	}
	return t
}

// writeLastCheck records now as the last-check time, creating the parent dir.
// Best-effort: a failure just means we may check again sooner.
func writeLastCheck(path string, now time.Time) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(now.Format(time.RFC3339)), 0o644)
}

// maybeAutoUpdate runs the best-effort startup update check for `outbox up`. It
// NEVER blocks or fails startup: a dev build, a container/homebrew install, a
// throttled window, or any network error all short-circuit quietly. When a newer
// release exists and this is a standalone install with auto_update on, it
// self-replaces and re-execs so the running `up` becomes the new version;
// otherwise it prints a one-line notice pointing at the right channel.
//
// It must run BEFORE the listener is bound: a successful self-replace re-execs,
// and the new process must be the one that owns the port.
func maybeAutoUpdate(cfg config.Config, out io.Writer) {
	if version == "dev" {
		return
	}
	// Opt-out short-circuits BEFORE any throttle or network I/O: auto_update:
	// false means the startup check is fully off (no daily GitHub request), not
	// "notify but don't apply". `outbox upgrade` remains the manual path.
	if !cfg.AutoUpdate {
		return
	}
	// Docker never self-updates on `up` (and `up` is not the container path
	// anyway); nothing actionable to print, so skip before any I/O.
	kind := installKindOf()
	if kind == kindDocker || kind == kindUnknown {
		return
	}

	// Throttle: check at most once per interval. Record the attempt as soon as it
	// becomes due — even on a network failure — so an offline restart doesn't pay
	// the timeout on every launch. Missing cache dir → run without persistence.
	if path, err := throttlePath(); err == nil {
		if !throttleDue(readLastCheck(path), time.Now(), updateCheckInterval) {
			return
		}
		_ = writeLastCheck(path, time.Now())
	}

	latest, err := latestRelease()
	if err != nil || !semverNewer(version, latest) {
		return
	}

	switch kind {
	case kindStandalone:
		// auto_update is on here (checked above), so apply.
		if err := selfReplace(latest); err != nil {
			// Leave the current binary running; surface nothing fatal.
			fmt.Fprintf(out, "outbox: self-update to v%s failed (%v) — continuing on v%s\n", latest, err, version)
			return
		}
		fmt.Fprintf(out, "outbox: updated v%s→v%s, restarting\n", version, latest)
		exe, err := os.Executable()
		if err == nil {
			// Replace this process image with the new binary; it re-runs `up` and
			// binds the port fresh. If exec fails we fall through and keep serving.
			_ = syscall.Exec(exe, os.Args, os.Environ())
		}
	case kindHomebrew:
		fmt.Fprintf(out, "outbox: v%s available — run `brew upgrade outbox-md`\n", latest)
	}
}
