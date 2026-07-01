package main

import (
	"fmt"
	"io"
	"os"

	"github.com/rajanrx/outbox-md/internal/registry"
	"golang.org/x/term"
)

// removeRow is one selectable line in the interactive remove list: a project name
// paired with one of its docs subpaths. Each project contributes one row per docs
// entry, so removal operates at docs granularity (not whole-project).
type removeRow struct {
	project string
	docs    string // the docs subpath as stored ("." = whole repo)
}

// label renders a row for display, e.g. "outbox-md · docs/specs".
func (r removeRow) label() string { return r.project + " · " + r.docs }

// buildRemoveRows flattens the registry into one row per (project, docs) pair, in
// registry order, so the multiselect can offer docs-granular removal.
func buildRemoveRows(projects []registry.Project) []removeRow {
	rows := make([]removeRow, 0, len(projects))
	for _, p := range projects {
		docs := p.Docs
		if len(docs) == 0 {
			docs = []string{"."}
		}
		for _, d := range docs {
			if d == "" {
				d = "."
			}
			rows = append(rows, removeRow{project: p.Name, docs: d})
		}
	}
	return rows
}

// removeInteractive runs the interactive multiselect: it lists every project and
// each of its docs entries as a tickable row, lets the user toggle a selection
// (space) and confirm (enter), then removes the selected docs entries — dropping
// any project whose last docs entry is removed. An empty registry or an empty
// selection prints a friendly message and returns nil (exit 0). stdin must be a
// terminal (the caller guards this).
func removeInteractive(out io.Writer, stdin io.Reader) error {
	file := registryPath()
	projects, err := registry.Load(file)
	if err != nil {
		return err
	}
	if len(projects) == 0 {
		fmt.Fprintln(out, "no projects registered — nothing to remove")
		return nil
	}
	rows := buildRemoveRows(projects)

	selected, ok, err := runMultiselect(out, stdin, rows)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(out, "cancelled — nothing removed")
		return nil
	}

	removals := make([]registry.DocRemoval, 0, len(selected))
	for i, on := range selected {
		if on {
			removals = append(removals, registry.DocRemoval{Project: rows[i].project, Docs: rows[i].docs})
		}
	}
	if len(removals) == 0 {
		fmt.Fprintln(out, "nothing selected — nothing removed")
		return nil
	}

	kept, applied := registry.ApplyRemovals(projects, removals)
	if err := registry.Save(file, kept); err != nil {
		return err
	}
	fmt.Fprintln(out, "removed:")
	for _, a := range applied {
		fmt.Fprintf(out, "  %s · %s\n", a.Project, a.Docs)
	}
	return nil
}

// runMultiselect drives the raw-mode checkbox list. It returns the per-row
// selection, and ok=false when the user cancelled (q / esc / Ctrl-C) so the caller
// makes no changes. The terminal is always restored on every exit path. When the
// reader is not an *os.File it returns ok=false (no changes) rather than blocking —
// belt-and-braces beside the caller's isTerminal guard.
func runMultiselect(out io.Writer, stdin io.Reader, rows []removeRow) (selected []bool, ok bool, err error) {
	f, isFile := stdin.(*os.File)
	if !isFile {
		return nil, false, nil
	}
	fd := int(f.Fd())
	state, err := term.MakeRaw(fd)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = term.Restore(fd, state) }()

	selected = make([]bool, len(rows))
	cursor := 0
	firstDraw := true
	for {
		drawMultiselect(out, rows, selected, cursor, firstDraw)
		firstDraw = false

		buf := make([]byte, 3)
		n, rerr := f.Read(buf)
		if rerr != nil {
			return nil, false, rerr
		}
		if n == 0 {
			continue
		}
		switch {
		// Arrow keys arrive as a 3-byte escape sequence (ESC [ A/B).
		case n >= 3 && buf[0] == 0x1b && buf[1] == '[':
			switch buf[2] {
			case 'A':
				cursor = wrap(cursor-1, len(rows))
			case 'B':
				cursor = wrap(cursor+1, len(rows))
			}
		// A lone ESC cancels.
		case n == 1 && buf[0] == 0x1b:
			finishMultiselect(out, len(rows))
			return nil, false, nil
		default:
			switch buf[0] {
			case 'k':
				cursor = wrap(cursor-1, len(rows))
			case 'j':
				cursor = wrap(cursor+1, len(rows))
			case ' ':
				selected[cursor] = !selected[cursor]
			case '\r', '\n':
				finishMultiselect(out, len(rows))
				return selected, true, nil
			case 'q', 0x03: // q or Ctrl-C
				finishMultiselect(out, len(rows))
				return nil, false, nil
			}
		}
	}
}

// wrap keeps the cursor within [0,n) with wraparound.
func wrap(i, n int) int {
	if n == 0 {
		return 0
	}
	return ((i % n) + n) % n
}

// header is the fixed hint line shown above the rows.
const multiselectHeader = "Select docs to remove — ↑/↓ move · space toggles · enter confirms · q/esc cancels"

// drawMultiselect renders the header + one line per row. On every redraw after the
// first it moves the cursor back up over the previously drawn block and clears
// downward, so the list updates in place. Uses \r\n line endings because raw mode
// disables the terminal's newline translation.
func drawMultiselect(out io.Writer, rows []removeRow, selected []bool, cursor int, firstDraw bool) {
	if !firstDraw {
		// Move up over the header + rows drawn last time and clear to end of screen.
		fmt.Fprintf(out, "\r\x1b[%dA\x1b[J", len(rows)+1)
	}
	fmt.Fprintf(out, "%s\r\n", multiselectHeader)
	for i, r := range rows {
		pointer := "  "
		if i == cursor {
			pointer = "> "
		}
		box := "[ ]"
		if selected[i] {
			box = "[x]"
		}
		fmt.Fprintf(out, "%s%s %s\r\n", pointer, box, r.label())
	}
}

// finishMultiselect leaves the rendered list on screen and drops the cursor onto a
// fresh line, so the subsequent (cooked-mode) result print starts cleanly below it.
func finishMultiselect(out io.Writer, _ int) {
	fmt.Fprint(out, "\r\n")
}
