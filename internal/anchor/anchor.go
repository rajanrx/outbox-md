package anchor

import (
	"github.com/rajanrx/outbox-md/internal/domain"
	"github.com/sergi/go-diff/diffmatchpatch"
)

// Remap carries an anchor from oldContent to newContent.
// ok=false means the anchored text was removed or altered (detached).
func Remap(oldContent, newContent string, a domain.Anchor) (domain.Anchor, bool) {
	dmp := diffmatchpatch.New()
	diffs := dmp.DiffMain(oldContent, newContent, false)
	ns, ok1 := mapPos(diffs, a.Start)
	ne, ok2 := mapPos(diffs, a.End)
	if !ok1 || !ok2 || ne <= ns {
		return domain.Anchor{}, false
	}
	out := domain.Anchor{Start: ns, End: ne}
	// Detachment guard: the anchored text must be preserved verbatim.
	if sub(newContent, out) != sub(oldContent, a) {
		return domain.Anchor{}, false
	}
	return out, true
}

// mapPos maps a rune position in text1 to text2. ok=false if it falls
// inside deleted text.
func mapPos(diffs []diffmatchpatch.Diff, pos int) (int, bool) {
	oldPos, newPos := 0, 0
	for _, d := range diffs {
		n := len([]rune(d.Text))
		switch d.Type {
		case diffmatchpatch.DiffEqual:
			if pos <= oldPos+n {
				return newPos + (pos - oldPos), true
			}
			oldPos += n
			newPos += n
		case diffmatchpatch.DiffDelete:
			if pos < oldPos+n {
				return newPos, false
			}
			oldPos += n
		case diffmatchpatch.DiffInsert:
			newPos += n
		}
	}
	if pos == oldPos {
		return newPos, true
	}
	return newPos, false
}

func sub(s string, a domain.Anchor) string {
	r := []rune(s)
	if a.Start < 0 || a.End > len(r) || a.Start > a.End {
		return ""
	}
	return string(r[a.Start:a.End])
}
