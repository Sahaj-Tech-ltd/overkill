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
