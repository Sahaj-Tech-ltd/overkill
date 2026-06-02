package term

import "testing"

func TestParseBackgroundReply_DarkBackground(t *testing.T) {
	dark, err := parseBackgroundReply([]byte("\x1b]11;rgb:0000/0000/0000\x07"))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !dark {
		t.Fatal("rgb:0/0/0 should be dark")
	}
}

func TestParseBackgroundReply_LightBackground(t *testing.T) {
	dark, err := parseBackgroundReply([]byte("\x1b]11;rgb:ffff/ffff/ffff\x07"))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if dark {
		t.Fatal("rgb:ffff/ffff/ffff should be light")
	}
}

func TestParseBackgroundReply_TerminalSolarizedDark(t *testing.T) {
	// solarized dark background ≈ #002b36 → 16-bit 0000/2b2b/3636
	dark, err := parseBackgroundReply([]byte("\x1b]11;rgb:0000/2b2b/3636\x07"))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !dark {
		t.Fatal("solarized dark should classify as dark")
	}
}

func TestParseBackgroundReply_Malformed(t *testing.T) {
	if _, err := parseBackgroundReply([]byte("garbage")); err == nil {
		t.Fatal("expected error on malformed reply")
	}
}

func TestParseColor_8bitHex(t *testing.T) {
	v, err := parseColor("ff")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if v < 0.99 || v > 1.0 {
		t.Fatalf("ff should be ~1.0, got %v", v)
	}
}

func TestParseColor_16bitHex(t *testing.T) {
	v, err := parseColor("ffff")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if v < 0.9999 || v > 1.0 {
		t.Fatalf("ffff should be ~1.0, got %v", v)
	}
}

func TestParseColor_MidScale(t *testing.T) {
	// 8-bit: 80/ff ≈ 0.502
	v, err := parseColor("80")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if v < 0.49 || v > 0.51 {
		t.Fatalf("80 should be ~0.502, got %v", v)
	}
}

func TestParseColor_BadHex(t *testing.T) {
	if _, err := parseColor("zzzz"); err == nil {
		t.Fatal("expected error on bad hex")
	}
}

func TestParseColor_ZeroLength(t *testing.T) {
	// Empty string: ParseUint("", 16, 32) returns error.
	if _, err := parseColor(""); err == nil {
		t.Fatal("expected error on empty hex string")
	}
}

func TestParseBackgroundReply_BackslashTerminator(t *testing.T) {
	// Some terminals terminate with ST (ESC \) instead of BEL (\x07).
	dark, err := parseBackgroundReply([]byte("\x1b]11;rgb:0000/0000/0000\\"))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !dark {
		t.Fatal("should be dark with backslash terminator")
	}
}

func TestParseBackgroundReply_MixedCaseHex(t *testing.T) {
	dark, err := parseBackgroundReply([]byte("\x1b]11;rgb:0a0a/BeEf/C0C0\x07"))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	// 0a0a≈0.039, BeEf≈0.749, C0C0≈0.753 → luma ≈ 0.2126*0.039+0.7152*0.749+0.0722*0.753 ≈ 0.599 → light
	if dark {
		t.Fatal("mixed case hex: expected light")
	}
}

func TestParseBackgroundReply_IncompleteRGB(t *testing.T) {
	// Only two channels instead of three
	if _, err := parseBackgroundReply([]byte("\x1b]11;rgb:0000/0000\x07")); err == nil {
		t.Fatal("expected error with only 2 channels")
	}
}
