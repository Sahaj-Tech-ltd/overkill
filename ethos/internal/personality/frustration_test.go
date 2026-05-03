package personality

import (
	"testing"
	"time"
)

func TestFrustration_NormalInputDoesNotFire(t *testing.T) {
	sink := &captureSink{}
	d := NewFrustrationDetector(sink, "s")
	if d.Observe("Could you check the test failure?") {
		t.Fatal("calm input should not fire")
	}
	if len(sink.calls) != 0 {
		t.Fatalf("unexpected alerts: %+v", sink.calls)
	}
}

func TestFrustration_ShoutingFires(t *testing.T) {
	sink := &captureSink{}
	d := NewFrustrationDetector(sink, "s")
	// All-caps + emphatic punctuation = score 2.
	if !d.Observe("WHY ARE YOU DOING THIS!!") {
		t.Fatal("shouting should fire")
	}
	if len(sink.calls) != 1 {
		t.Fatalf("got %d alerts want 1", len(sink.calls))
	}
	if sink.calls[0].Type != "frustration_signal" {
		t.Fatalf("type = %q", sink.calls[0].Type)
	}
}

func TestFrustration_RepeatedRequestFires(t *testing.T) {
	sink := &captureSink{}
	d := NewFrustrationDetector(sink, "s")
	d.Observe("fix the bug")
	if !d.Observe("FIX THE BUG NOW!!") {
		t.Fatal("repeat with emphasis should fire")
	}
	if len(sink.calls) == 0 {
		t.Fatal("expected an alert")
	}
}

func TestFrustration_LexiconFires(t *testing.T) {
	sink := &captureSink{}
	d := NewFrustrationDetector(sink, "s")
	if !d.Observe("this is broken, just do it!!") {
		t.Fatal("lexicon + punctuation should fire")
	}
}

func TestFrustration_CooldownRespected(t *testing.T) {
	sink := &captureSink{}
	d := NewFrustrationDetector(sink, "s")
	d.cooldown = 200 * time.Millisecond
	if !d.Observe("THIS IS BROKEN!!") {
		t.Fatal("first should fire")
	}
	if d.Observe("STILL BROKEN WTF!!") {
		t.Fatal("within cooldown should not fire")
	}
	time.Sleep(220 * time.Millisecond)
	if !d.Observe("WHY!! WHY?!") {
		t.Fatal("after cooldown should fire again")
	}
	if len(sink.calls) != 2 {
		t.Fatalf("got %d alerts want 2", len(sink.calls))
	}
}

func TestFrustration_NilSinkSafe(t *testing.T) {
	d := NewFrustrationDetector(nil, "")
	// Should not panic; returns false because sink is nil even if score >= 2.
	d.Observe("WHY!!")
}

func TestFrustration_EmptyInput(t *testing.T) {
	sink := &captureSink{}
	d := NewFrustrationDetector(sink, "s")
	if d.Observe("   ") {
		t.Fatal("empty input should not fire")
	}
}
