// Package watcher live-reloads served .md files. It watches every spec directory
// of every served project with fsnotify and, on a create/write/rename/remove,
// reconciles the changed file into the document store and broadcasts a
// browser-only docs.changed SSE event — so files created, edited or deleted
// while the server runs appear in the UI without a restart.
//
// Design notes:
//
//   - fsnotify is not recursive, so every subdirectory of each spec dir is added
//     to the watch set, and a newly-created directory is added on the fly (then
//     re-walked, to catch files dropped into it before the watch attached).
//   - The DB and dotfolders are skipped everywhere a watch is added — in
//     single-folder mode the SQLite database lives at root/.outbox/outbox.db,
//     under the watched tree, and its journal churn would otherwise storm us.
//     This mirrors importTree's skip rules exactly.
//   - Only paths that are .md AND allowed by the project's Serves() whitelist are
//     acted on. The WATCH decision and the ACT decision are separate: every
//     subdir is watched (so a later-created served subdir is still caught) but a
//     file only reconciles when it is served.
//   - Events are debounced per file path, so an editor's multi-write + temp-rename
//     save coalesces to a single reconcile — and delete-then-create saves land as
//     one "update", not a delete/create flicker. The reconcile stats the path: it
//     exists → SyncFile (create or new version), it is gone → RemoveFile.
//   - docs.changed is fired on the SSE hub DIRECTLY, never through the webhook
//     fan-out, so the watcher can never trigger the auto-reply engine (the
//     no-self-retrigger boundary stays intact).
package watcher

import (
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// DefaultDebounce is the quiet window a file must go idle before it is
// reconciled. It is long enough to swallow an editor's burst of writes and its
// temp-file rename on save, short enough to feel live.
const DefaultDebounce = 400 * time.Millisecond

// Syncer is the store-facing seam the watcher drives. *service.Service satisfies
// it; tests supply a counting double to assert coalescing without wall-clock
// races.
type Syncer interface {
	// SyncFile upserts the file keyed (project, path) with content; changed is
	// true when the store actually changed (new doc or new version).
	SyncFile(project, path, content string) (changed bool, err error)
	// RemoveFile drops the document keyed (project, path); removed is false when
	// no such document existed.
	RemoveFile(project, path string) (removed bool, err error)
	// ProjectServes reports whether path (relative to the project root) is inside
	// the project's active sources whitelist.
	ProjectServes(project, path string) bool
}

// Notifier is the browser-only SSE sink. *sse.Hub satisfies it. It is
// deliberately NOT the webhook fan-out: the watcher must not reach the runner or
// the auto-reply engine.
type Notifier interface {
	Fire(event string, payload any)
}

// EventDocsChanged is the browser-only SSE event a filesystem change broadcasts.
const EventDocsChanged = "docs.changed"

// changePayload is the docs.changed body. The UI re-fetches the doc list on any
// docs.changed, so the fields are advisory (useful for logging/filtering).
type changePayload struct {
	Project string `json:"project"`
	Path    string `json:"path"`
	Action  string `json:"action"` // "upsert" | "remove"
}

// Project is the minimal view of a served project the watcher needs: its name,
// its root (keys are relative to this), and the spec dirs to watch.
type Project struct {
	Name     string
	Root     string
	SpecDirs []string
}

// dirCtx tags a watched directory with the project it belongs to, so an event
// path resolves back to (project, root-relative key) in O(1).
type dirCtx struct {
	project string
	root    string
}

// Watcher watches spec dirs and reconciles changes into the store.
type Watcher struct {
	fsw      *fsnotify.Watcher
	sync     Syncer
	notify   Notifier
	debounce time.Duration

	mu     sync.Mutex
	dirs   map[string]dirCtx        // watched dir -> owning project
	timers map[string]*time.Timer   // abs .md path -> pending reconcile
	closed bool

	done chan struct{}
}

// New builds a Watcher over the given projects and adds an initial watch for
// every (non-hidden) directory under each spec dir. It returns a nil Watcher and
// the error only if fsnotify itself is unavailable; the caller treats that as
// non-fatal (the server still serves, just without live reload).
func New(projects []Project, syncer Syncer, notify Notifier, debounce time.Duration) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if debounce <= 0 {
		debounce = DefaultDebounce
	}
	w := &Watcher{
		fsw:      fsw,
		sync:     syncer,
		notify:   notify,
		debounce: debounce,
		dirs:     make(map[string]dirCtx),
		timers:   make(map[string]*time.Timer),
		done:     make(chan struct{}),
	}
	for _, p := range projects {
		for _, dir := range p.SpecDirs {
			w.addTree(dir, dirCtx{project: p.Name, root: p.Root}, false)
		}
	}
	log.Printf("watcher: watching %d dirs across %d project(s)", w.dirCount(), len(projects))
	return w, nil
}

// Start launches the event loop. Call exactly once.
func (w *Watcher) Start() {
	go w.loop()
}

// Close stops the event loop, cancels every pending reconcile timer, and closes
// the fsnotify watcher. It is idempotent and returns only after the event
// goroutine has exited, so there is no goroutine leak.
func (w *Watcher) Close() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closed = true
	for _, t := range w.timers {
		t.Stop()
	}
	w.timers = map[string]*time.Timer{}
	w.mu.Unlock()

	err := w.fsw.Close() // closes Events → loop() returns
	<-w.done
	return err
}

func (w *Watcher) dirCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.dirs)
}

// addTree walks root, adding a watch for it and every non-hidden subdirectory,
// each tagged with ctx. When scan is true it also schedules a reconcile for every
// .md file found — used when a directory appears at runtime (mkdir/mv) so files
// created inside it before the watch attached are not missed. The initial
// startup walk passes scan=false because buildServer already imported those
// files.
func (w *Watcher) addTree(root string, ctx dirCtx, scan bool) {
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entry: skip, never abort the walk
		}
		if d.IsDir() {
			// Skip the DB folder and any dot-directory (except the walk root
			// itself), mirroring importTree so journal/hidden churn never watches.
			if d.Name() == ".outbox" || (strings.HasPrefix(d.Name(), ".") && p != root) {
				return fs.SkipDir
			}
			w.addDir(p, ctx)
			return nil
		}
		if scan && strings.HasSuffix(d.Name(), ".md") {
			w.schedule(p, ctx)
		}
		return nil
	})
}

// addDir registers a single directory watch under ctx. A failure to watch (e.g.
// a race where the dir vanished) is logged and ignored.
func (w *Watcher) addDir(dir string, ctx dirCtx) {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	if _, ok := w.dirs[dir]; ok {
		w.mu.Unlock()
		return
	}
	w.dirs[dir] = ctx
	w.mu.Unlock()
	if err := w.fsw.Add(dir); err != nil {
		w.mu.Lock()
		delete(w.dirs, dir)
		w.mu.Unlock()
	}
}

// loop consumes fsnotify events until the watcher is closed.
func (w *Watcher) loop() {
	defer close(w.done)
	for {
		select {
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			w.handle(ev)
		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			log.Printf("watcher: %v", err)
		}
	}
}

// handle turns one raw fsnotify event into watch-set maintenance and/or a
// debounced reconcile.
func (w *Watcher) handle(ev fsnotify.Event) {
	dir := filepath.Dir(ev.Name)
	w.mu.Lock()
	ctx, known := w.dirs[dir]
	w.mu.Unlock()
	if !known {
		return // event outside any watched (served) tree
	}

	// A newly-created directory joins the watch set (and is scanned for .md files
	// dropped in before the watch attached). fsnotify does not recurse for us.
	if ev.Op&fsnotify.Create != 0 {
		if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
			name := filepath.Base(ev.Name)
			if name != ".outbox" && !strings.HasPrefix(name, ".") {
				w.addTree(ev.Name, ctx, true)
			}
			return
		}
	}

	if !strings.HasSuffix(ev.Name, ".md") {
		return
	}
	w.schedule(ev.Name, ctx)
}

// schedule debounces a reconcile for path: every event resets a per-path timer,
// so a burst of writes (and a temp→rename save) collapses into a single
// reconcile once the path goes quiet for w.debounce.
func (w *Watcher) schedule(path string, ctx dirCtx) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	if t, ok := w.timers[path]; ok {
		t.Stop()
	}
	w.timers[path] = time.AfterFunc(w.debounce, func() { w.reconcile(path, ctx) })
}

// reconcile is the debounced action for one path. It re-stats the file (so the
// LAST state after a burst wins): present → SyncFile, absent → RemoveFile. It
// gates the action on the project's Serves() whitelist, keyed relative to the
// project root exactly as buildServer imports. It broadcasts docs.changed only
// when the store actually changed.
func (w *Watcher) reconcile(path string, ctx dirCtx) {
	w.mu.Lock()
	delete(w.timers, path)
	closed := w.closed
	w.mu.Unlock()
	if closed {
		return
	}

	key, err := filepath.Rel(ctx.root, path)
	if err != nil {
		return
	}
	key = filepath.ToSlash(key)
	if !w.sync.ProjectServes(ctx.project, key) {
		return // not in this project's whitelist — invisible, ignore
	}

	if b, err := os.ReadFile(path); err == nil {
		changed, err := w.sync.SyncFile(ctx.project, key, string(b))
		if err != nil {
			log.Printf("watcher: sync %s: %v", key, err)
			return
		}
		if changed {
			log.Printf("watcher: imported %s", key)
			w.broadcast(ctx.project, key, "upsert")
		}
		return
	} else if !os.IsNotExist(err) {
		log.Printf("watcher: read %s: %v", key, err)
		return
	}

	// The file is gone (deleted or renamed away): drop it from the store.
	removed, err := w.sync.RemoveFile(ctx.project, key)
	if err != nil {
		log.Printf("watcher: remove %s: %v", key, err)
		return
	}
	if removed {
		log.Printf("watcher: removed %s", key)
		w.broadcast(ctx.project, key, "remove")
	}
}

func (w *Watcher) broadcast(project, path, action string) {
	if w.notify == nil {
		return
	}
	w.notify.Fire(EventDocsChanged, changePayload{Project: project, Path: path, Action: action})
}
