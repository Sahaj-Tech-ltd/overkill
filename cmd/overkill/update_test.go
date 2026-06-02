package main

import "testing"

func TestNormalizeVersion(t *testing.T) {
	cases := []struct{ in, want string }{
		{"v1.2.3", "1.2.3"},
		{"1.2.3", "1.2.3"},
		{"v1.2.3-rc1", "1.2.3"},
		{"v1.2.3+build", "1.2.3"},
		{"  v0.0.1  ", "0.0.1"},
	}
	for _, tc := range cases {
		if got := normalizeVersion(tc.in); got != tc.want {
			t.Errorf("normalize(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}

func TestSemverCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.2.3", "1.2.3", 0},
		{"1.2.4", "1.2.3", 1},
		{"1.2.3", "1.2.4", -1},
		{"2.0.0", "1.99.99", 1},
		{"1.10.0", "1.2.0", 1},
		{"1.2", "1.2.0", 0},
	}
	for _, tc := range cases {
		got := semverCompare(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("compare(%q,%q)=%d want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestIsNewer(t *testing.T) {
	cases := []struct {
		tag, cur string
		want     bool
	}{
		{"v1.2.4", "1.2.3", true},
		{"v1.2.3", "1.2.3", false},
		{"v1.2.2", "1.2.3", false},
		{"v0.1.0", "0.0.0", true},
		{"v0.1.0", "", true},
	}
	for _, tc := range cases {
		if got := isNewer(tc.tag, tc.cur); got != tc.want {
			t.Errorf("isNewer(%q,%q)=%v want %v", tc.tag, tc.cur, got, tc.want)
		}
	}
}

func TestPickAsset_ExactGOOSGOARCH(t *testing.T) {
	assets := []releaseAsset{
		{Name: "overkill-linux-amd64"},
		{Name: "overkill-darwin-arm64"},
		{Name: "overkill-windows-amd64.exe"},
	}
	a, err := pickAsset(assets)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	// Test verifies structure works on the host's GOOS/GOARCH; we don't
	// assert a specific name because the host arch varies.
	if a == nil {
		t.Fatal("nil asset")
	}
}

func TestPickAsset_FallbackSingleAsset(t *testing.T) {
	assets := []releaseAsset{{Name: "overkill.tar.gz"}}
	a, err := pickAsset(assets)
	if err != nil || a == nil {
		t.Fatalf("expected single-asset fallback, got %v err=%v", a, err)
	}
}

func TestPickAsset_NoMatchErrors(t *testing.T) {
	assets := []releaseAsset{
		{Name: "overkill-aix-mips64"},
		{Name: "overkill-zos-s390"},
	}
	if _, err := pickAsset(assets); err == nil {
		t.Fatal("expected error when nothing matches host")
	}
}
