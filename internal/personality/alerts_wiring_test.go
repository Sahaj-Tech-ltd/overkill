package personality

import "testing"

type captureSink struct {
	calls []struct {
		Type, Message, SessionID string
	}
}

func (c *captureSink) Create(alertType, message, sessionID string) error {
	c.calls = append(c.calls, struct {
		Type, Message, SessionID string
	}{alertType, message, sessionID})
	return nil
}

func TestTransparency_FiresFrustrationAlert(t *testing.T) {
	te := NewTransparencyEngine("gpt-4o")
	sink := &captureSink{}
	te.SetAlertSink(sink, "session-1")
	te.RecordFailure("debugging", "gpt-4o")
	te.RecordFailure("debugging", "gpt-4o")
	if _, ok := te.Check("debugging"); !ok {
		t.Fatal("expected check to warn")
	}
	if len(sink.calls) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(sink.calls))
	}
	if sink.calls[0].Type != "frustration_signal" {
		t.Errorf("expected frustration_signal, got %q", sink.calls[0].Type)
	}
	if sink.calls[0].SessionID != "session-1" {
		t.Errorf("session id not propagated: %q", sink.calls[0].SessionID)
	}
}

func TestBlindspot_FiresPatternDetectedAlert(t *testing.T) {
	bsd := NewBlindSpotDetector()
	bsd.Threshold = 2
	sink := &captureSink{}
	bsd.SetAlertSink(sink, "session-2")
	bsd.Observe("fix")
	bsd.Observe("fix")
	if _, ok := bsd.Check(); !ok {
		t.Fatal("expected check to trip")
	}
	if len(sink.calls) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(sink.calls))
	}
	if sink.calls[0].Type != "pattern_detected" {
		t.Errorf("expected pattern_detected, got %q", sink.calls[0].Type)
	}
}
