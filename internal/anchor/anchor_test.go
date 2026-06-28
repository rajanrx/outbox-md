package anchor

import (
	"testing"

	"github.com/rajanrx/outbox-md/internal/domain"
)

// "Hello world" → "world" is runes [6,11).
func TestRemap(t *testing.T) {
	const old = "Hello world"
	world := domain.Anchor{Start: 6, End: 11}

	cases := []struct {
		name   string
		newC   string
		want   domain.Anchor
		wantOK bool
	}{
		{"no change", "Hello world", domain.Anchor{Start: 6, End: 11}, true},
		{"insert before", "Say Hello world", domain.Anchor{Start: 10, End: 15}, true},
		{"append after", "Hello world!!!", domain.Anchor{Start: 6, End: 11}, true},
		{"anchored text replaced", "Hello there", domain.Anchor{}, false},
		{"anchored text deleted", "Hello ", domain.Anchor{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := Remap(old, c.newC, world)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v", ok, c.wantOK)
			}
			if ok && got != c.want {
				t.Fatalf("anchor = %+v, want %+v", got, c.want)
			}
		})
	}
}
