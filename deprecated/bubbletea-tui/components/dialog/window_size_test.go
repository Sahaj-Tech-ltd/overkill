package dialog

import "testing"

func TestWindowSize_ClampsAndCaps(t *testing.T) {
	cases := []struct {
		totalH, want int
	}{
		{totalH: 0, want: 5},   // floor
		{totalH: 10, want: 5},  // floor (10-8=2 → 5)
		{totalH: 20, want: 12}, // ordinary (20-8=12)
		{totalH: 30, want: 15}, // capped (30-8=22 → 15)
	}
	for _, c := range cases {
		if got := WindowSize(c.totalH); got != c.want {
			t.Errorf("WindowSize(%d): got %d want %d", c.totalH, got, c.want)
		}
	}
}
