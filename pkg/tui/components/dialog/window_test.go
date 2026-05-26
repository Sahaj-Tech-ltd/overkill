package dialog

import (
	"reflect"
	"testing"
)

func TestWindow(t *testing.T) {
	items := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	tests := []struct {
		name       string
		cursor     int
		max        int
		wantVis    []string
		wantBefore int
		wantAfter  int
	}{
		{"no cap returns all", 0, 0, items, 0, 0},
		{"cap >= len returns all", 5, 100, items, 0, 0},
		{"window at start", 0, 4, []string{"a", "b", "c", "d"}, 0, 6},
		{"window centers around cursor", 5, 4, []string{"d", "e", "f", "g"}, 3, 3},
		{"window clamps to end", 9, 4, []string{"g", "h", "i", "j"}, 6, 0},
		{"cursor negative clamps to 0", -3, 3, []string{"a", "b", "c"}, 0, 7},
		{"cursor over end clamps", 999, 3, []string{"h", "i", "j"}, 7, 0},
		{"empty input", 0, 5, nil, 0, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := items
			if tc.name == "empty input" {
				in = nil
			}
			vis, before, after := Window(in, tc.cursor, tc.max)
			if !reflect.DeepEqual(vis, tc.wantVis) {
				t.Errorf("visible = %v, want %v", vis, tc.wantVis)
			}
			if before != tc.wantBefore {
				t.Errorf("before = %d, want %d", before, tc.wantBefore)
			}
			if after != tc.wantAfter {
				t.Errorf("after = %d, want %d", after, tc.wantAfter)
			}
		})
	}
}
