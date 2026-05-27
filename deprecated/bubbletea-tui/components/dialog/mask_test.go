package dialog

import "testing"

func TestMaskKey(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		visible int
		want    string
	}{
		{"empty", "", 5, ""},
		{"shorter than visible", "abc", 5, "abc"},
		{"exact length", "abcde", 5, "abcde"},
		{"masks all but last 5", "sk-ant-api03-abc123XYZ7Q9", 5, "********************YZ7Q9"},
		{"visible 0 fully masks", "secret", 0, "******"},
		{"visible negative fully masks", "secret", -1, "******"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := MaskKey(tc.in, tc.visible)
			if got != tc.want {
				t.Errorf("MaskKey(%q,%d) = %q, want %q", tc.in, tc.visible, got, tc.want)
			}
		})
	}
}
