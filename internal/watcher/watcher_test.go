package watcher

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rajanrx/outbox-md/internal/config"
	"github.com/rajanrx/outbox-md/internal/service"
	"github.com/rajanrx/outbox-md/internal/store"
)

// spyNotifier records every event fired on it, so tests can assert docs.changed
// was (or was NOT) broadcast — and prove the watcher only ever reaches this
// browser-only sink, never a webhook/auto-reply path.
type spyNotifier struct {
	mu     sync.Mutex
	events []string
	paths  []string
}

func (s *spyNotifier) Fire(event string, payload any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	if p, ok := payload.(changePayload); ok {
		s.paths = append(s.paths, p.Path)
	}
}

func (s *spyNotifier) count(event string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, e := range s.events {
		if e == event {
			n++
		}
	}
	return n
}

func (s *spyNotifier) total() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.events)
}

// eventually polls cond until true or the deadline, so tests never sleep a fixed
// (flaky) amount for a real fsnotify + debounce round-trip.
func eventually(t *testing.T, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(15 * time.Millisecond)
	}
	return cond()
}

// realSvc builds a store-backed service serving a single unrestricted folder
// (project ""), with a real disk-writing writeFile so the accept path exercises
// the same atomic write the watcher observes.
func openStore(t *testing.T) *store.Store {
	t.Helper()
	// A per-test on-disk database (unique temp dir) — never the shared in-memory
	// DSN, which would leak documents across tests in the same process.
	st, err := store.Open("file:" + filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func realSvc(t *testing.T) (*service.Service, *store.Store) {
	t.Helper()
	st := openStore(t)
	svc := service.New(st, func(_, _, _ string) error { return nil })
	svc.SetProjectSources(config.ProjectSources{"": config.Coverage{}})
	return svc, st
}

func startWatcher(t *testing.T, root string, svc Syncer, notify Notifier, debounce time.Duration) *Watcher {
	t.Helper()
	w, err := New([]Project{{Name: "", Root: root, SpecDirs: []string{root}}}, svc, notify, debounce)
	if err != nil {
		t.Fatal(err)
	}
	w.Start()
	t.Cleanup(func() { _ = w.Close() })
	return w
}

func versionCount(t *testing.T, st *store.Store, docID string) int {
	t.Helper()
	var n int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM versions WHERE doc_id=?`, docID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestCreateImportsAndBroadcasts: a .md created in a watched served dir lands in
// the store and fires exactly one docs.changed.
func TestCreateImportsAndBroadcasts(t *testing.T) {
	dir := t.TempDir()
	svc, st := realSvc(t)
	spy := &spyNotifier{}
	startWatcher(t, dir, svc, spy, 40*time.Millisecond)

	if err := os.WriteFile(filepath.Join(dir, "new.md"), []byte("# new\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ok := eventually(t, func() bool {
		_, found, _ := st.GetDocumentByPath("", "new.md")
		return found
	})
	if !ok {
		t.Fatal("new.md never imported into the store")
	}
	if !eventually(t, func() bool { return spy.count(EventDocsChanged) >= 1 }) {
		t.Fatalf("no docs.changed broadcast, events=%v", spy.events)
	}
}

// TestCreateOutsideWhitelistIgnored: a project narrowed to sources=[specs] must
// ignore a file created outside that subtree, even though the whole root is
// watched.
func TestCreateOutsideWhitelistIgnored(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "specs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "other"), 0o755); err != nil {
		t.Fatal(err)
	}
	st := openStore(t)
	svc := service.New(st, func(_, _, _ string) error { return nil })
	svc.SetProjectSources(config.ProjectSources{"": config.Coverage{Sources: []string{"specs"}}})

	spy := &spyNotifier{}
	startWatcher(t, dir, svc, spy, 40*time.Millisecond)

	// Outside the whitelist → ignored entirely.
	if err := os.WriteFile(filepath.Join(dir, "other", "x.md"), []byte("secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Inside the whitelist → imported. Firing this AFTER proves the watcher was
	// live the whole time (so the ignore above wasn't just a missed event).
	if err := os.WriteFile(filepath.Join(dir, "specs", "y.md"), []byte("served\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !eventually(t, func() bool {
		_, found, _ := st.GetDocumentByPath("", "specs/y.md")
		return found
	}) {
		t.Fatal("specs/y.md (whitelisted) never imported")
	}
	if _, found, _ := st.GetDocumentByPath("", "other/x.md"); found {
		t.Fatal("other/x.md was imported despite being outside the whitelist")
	}
}

// TestDeleteRemoves: deleting a served .md drops its document and fires
// docs.changed(remove).
func TestDeleteRemoves(t *testing.T) {
	dir := t.TempDir()
	svc, st := realSvc(t)
	path := filepath.Join(dir, "gone.md")
	if err := os.WriteFile(path, []byte("bye\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Seed the store as startup import would.
	if _, err := svc.SyncFile("", "gone.md", "bye\n"); err != nil {
		t.Fatal(err)
	}

	spy := &spyNotifier{}
	startWatcher(t, dir, svc, spy, 40*time.Millisecond)

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if !eventually(t, func() bool {
		_, found, _ := st.GetDocumentByPath("", "gone.md")
		return !found
	}) {
		t.Fatal("gone.md still in store after delete")
	}
	if spy.count(EventDocsChanged) < 1 {
		t.Fatalf("no docs.changed on delete, events=%v", spy.events)
	}
}

// countingSyncer counts SyncFile calls per path so the debounce test is
// deterministic — a burst of writes must collapse to ≤1 reimport per file.
type countingSyncer struct {
	mu    sync.Mutex
	syncs map[string]int
}

func (c *countingSyncer) SyncFile(_, path, _ string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.syncs == nil {
		c.syncs = map[string]int{}
	}
	c.syncs[path]++
	return true, nil
}
func (c *countingSyncer) RemoveFile(_, _ string) (bool, error) { return true, nil }
func (c *countingSyncer) ProjectServes(_, _ string) bool       { return true }

func (c *countingSyncer) calls(path string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.syncs[path]
}

// TestDebounceCoalescesBurst: many rapid writes to one file within the debounce
// window reconcile exactly once.
func TestDebounceCoalescesBurst(t *testing.T) {
	dir := t.TempDir()
	cs := &countingSyncer{}
	spy := &spyNotifier{}
	// A comfortably long window so the whole burst lands inside it.
	startWatcher(t, dir, cs, spy, 200*time.Millisecond)

	path := filepath.Join(dir, "burst.md")
	for i := 0; i < 12; i++ {
		if err := os.WriteFile(path, []byte("v"), 0o644); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !eventually(t, func() bool { return cs.calls("burst.md") >= 1 }) {
		t.Fatal("burst.md never reconciled")
	}
	// Let any erroneous extra timers fire, then assert coalescing held.
	time.Sleep(300 * time.Millisecond)
	if n := cs.calls("burst.md"); n != 1 {
		t.Fatalf("burst coalesced to %d reimports, want exactly 1", n)
	}
}

// TestSelfWriteDoesNotLoop pins the subtlest failure mode: the server's OWN
// disk write (temp file + rename, exactly what accept/reapprove do) is seen by
// the watcher, but because the bytes match the current version it must add NO new
// version and fire NO docs.changed — otherwise every accept would loop.
func TestSelfWriteDoesNotLoop(t *testing.T) {
	dir := t.TempDir()
	svc, st := realSvc(t)
	path := filepath.Join(dir, "a.md")
	if err := os.WriteFile(path, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SyncFile("", "a.md", "v1"); err != nil {
		t.Fatal(err)
	}
	doc, _, _ := st.GetDocumentByPath("", "a.md")
	before := versionCount(t, st, doc.ID)

	spy := &spyNotifier{}
	startWatcher(t, dir, svc, spy, 40*time.Millisecond)

	// Simulate the server's atomic self-write: identical content, temp + rename.
	tmp := filepath.Join(dir, ".outbox-tmp-a")
	if err := os.WriteFile(tmp, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}

	// Give the debounce + reconcile ample time to (wrongly) act.
	time.Sleep(300 * time.Millisecond)
	if got := versionCount(t, st, doc.ID); got != before {
		t.Fatalf("self-write added a version: had %d, now %d", before, got)
	}
	if spy.count(EventDocsChanged) != 0 {
		t.Fatalf("self-write fired docs.changed %d time(s), want 0", spy.count(EventDocsChanged))
	}
}

// TestNewSubdirJoinsWatch: a file created in a subdirectory that did not exist at
// startup is still caught — the new dir is added to the watch set on the fly.
func TestNewSubdirJoinsWatch(t *testing.T) {
	dir := t.TempDir()
	svc, st := realSvc(t)
	spy := &spyNotifier{}
	startWatcher(t, dir, svc, spy, 40*time.Millisecond)

	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	// Small settle so the CREATE(dir) event attaches the watch before the file.
	time.Sleep(60 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(sub, "c.md"), []byte("deep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !eventually(t, func() bool {
		_, found, _ := st.GetDocumentByPath("", "sub/c.md")
		return found
	}) {
		t.Fatal("sub/c.md in a runtime-created dir was never imported")
	}
}

// TestWatcherFiresOnlyDocsChanged proves the no-self-retrigger boundary: the only
// event the watcher ever emits is docs.changed on the browser hub. It never emits
// a comment.* / suggestion.* governance event, so it can never enqueue an
// auto-reply run (which is driven by the webhook fan-out, a different sink).
func TestWatcherFiresOnlyDocsChanged(t *testing.T) {
	dir := t.TempDir()
	svc, _ := realSvc(t)
	spy := &spyNotifier{}
	startWatcher(t, dir, svc, spy, 40*time.Millisecond)

	if err := os.WriteFile(filepath.Join(dir, "z.md"), []byte("z\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !eventually(t, func() bool { return spy.total() >= 1 }) {
		t.Fatal("watcher fired no events at all")
	}
	time.Sleep(100 * time.Millisecond)
	if spy.count(EventDocsChanged) != spy.total() {
		t.Fatalf("watcher emitted a non-docs.changed event: %v", spy.events)
	}
}
