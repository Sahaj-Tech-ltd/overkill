package halluscan

import (
	"strings"
	"testing"
)

func TestScan_FlagsUnverifiedIdentifier(t *testing.T) {
	s := NewScanner()
	content := "Use the `HallucinatedFunc` to do this."
	evidence := "no mention of that function here"
	res := s.Scan(content, evidence)

	if !strings.Contains(res.Annotated, "[?]") {
		t.Errorf("expected [?] annotation: %q", res.Annotated)
	}
	if len(res.Flagged) != 1 || res.Flagged[0] != "hallucinatedfunc" {
		t.Errorf("flagged list: %v", res.Flagged)
	}
}

func TestScan_PassesIdentifierInEvidence(t *testing.T) {
	s := NewScanner()
	content := "The `BcryptHash` function returns a hash."
	evidence := "earlier I called BcryptHash in the auth module"
	res := s.Scan(content, evidence)

	if strings.Contains(res.Annotated, "[?]") {
		t.Errorf("identifier IS in evidence; should not flag: %q", res.Annotated)
	}
	if len(res.Flagged) != 0 {
		t.Errorf("flagged: %v", res.Flagged)
	}
}

func TestScan_CaseInsensitiveEvidenceMatch(t *testing.T) {
	s := NewScanner()
	content := "Calls `BcryptHash` first."
	evidence := "BCRYPTHASH appeared in grep output"
	res := s.Scan(content, evidence)
	if strings.Contains(res.Annotated, "[?]") {
		t.Errorf("case-insensitive match failed: %q", res.Annotated)
	}
}

func TestScan_StopwordsNotFlagged(t *testing.T) {
	s := NewScanner()
	content := "Use `bash` and `grep` and `read` here."
	evidence := "no mention"
	res := s.Scan(content, evidence)
	if strings.Contains(res.Annotated, "[?]") {
		t.Errorf("stopwords should never flag: %q", res.Annotated)
	}
	if len(res.Flagged) != 0 {
		t.Errorf("flagged: %v", res.Flagged)
	}
}

func TestScan_NoBackticksNoScan(t *testing.T) {
	s := NewScanner()
	content := "Use HallucinatedFunc to do this."
	evidence := "no mention"
	res := s.Scan(content, evidence)
	// Bare-word references are an accepted blind spot — no flag.
	if strings.Contains(res.Annotated, "[?]") {
		t.Errorf("bare word should NOT flag: %q", res.Annotated)
	}
}

func TestScan_MultipleIdentifiers(t *testing.T) {
	s := NewScanner()
	content := "Use `Foo` then `Bar` then `Baz`."
	evidence := "Bar is real"
	res := s.Scan(content, evidence)
	// Foo + Baz should flag; Bar should not.
	if strings.Count(res.Annotated, "[?]") != 2 {
		t.Errorf("want 2 markers, got %d: %q", strings.Count(res.Annotated, "[?]"), res.Annotated)
	}
	if len(res.Flagged) != 2 {
		t.Errorf("flagged: %v", res.Flagged)
	}
}

func TestScan_CapAnnotations(t *testing.T) {
	s := NewScanner()
	s.MaxAnnotations = 3
	// 7 distinct 3+ char identifiers — only the first 3 should get
	// the [?] annotation per the cap.
	content := "`Alpha` `Bravo` `Charlie` `Delta` `Echo` `Foxtrot` `Golf`"
	evidence := ""
	res := s.Scan(content, evidence)
	if strings.Count(res.Annotated, "[?]") != 3 {
		t.Errorf("want 3 annotations (capped), got %d: %s",
			strings.Count(res.Annotated, "[?]"), res.Annotated)
	}
}

func TestScan_PreservesOriginalFormatting(t *testing.T) {
	s := NewScanner()
	content := "Step 1: `Thing`. Step 2: keep going."
	evidence := ""
	res := s.Scan(content, evidence)
	// Annotation must NOT mangle surrounding punctuation.
	if !strings.Contains(res.Annotated, "`Thing` [?].") {
		t.Errorf("annotation should land after the backtick run, before punctuation context: %q", res.Annotated)
	}
}

func TestScan_EmptyContentReturnsEmpty(t *testing.T) {
	s := NewScanner()
	res := s.Scan("", "evidence")
	if res.Annotated != "" {
		t.Errorf("empty content: %q", res.Annotated)
	}
	if len(res.Flagged) != 0 {
		t.Errorf("flagged should be empty: %v", res.Flagged)
	}
}

func TestScan_OneCharIdentifierNotMatched(t *testing.T) {
	// regex requires 3+ chars to avoid noise like `r` / `id`.
	s := NewScanner()
	content := "Use `id` here."
	evidence := ""
	res := s.Scan(content, evidence)
	if strings.Contains(res.Annotated, "[?]") {
		t.Errorf("short identifier should not match: %q", res.Annotated)
	}
}

func TestScan_DottedIdentifierMatches(t *testing.T) {
	s := NewScanner()
	content := "Calls `bcrypt.HashPassword` to derive the hash."
	evidence := ""
	res := s.Scan(content, evidence)
	if !strings.Contains(res.Annotated, "[?]") {
		t.Errorf("dotted identifier should flag: %q", res.Annotated)
	}
}

func TestScan_DedupsRepeats(t *testing.T) {
	s := NewScanner()
	content := "`Foo` then `Foo` again and `Foo` once more"
	evidence := ""
	res := s.Scan(content, evidence)
	if len(res.Flagged) != 1 {
		t.Errorf("repeats should dedupe to 1 unique flagged id: %v", res.Flagged)
	}
	// But annotations land on each occurrence (until cap).
	if strings.Count(res.Annotated, "[?]") < 3 {
		t.Errorf("each occurrence should be annotated: %q", res.Annotated)
	}
}

func TestScan_FirstOccurrenceOrderingInFlagged(t *testing.T) {
	s := NewScanner()
	content := "Use `Zulu` first, then `Alpha`."
	evidence := ""
	res := s.Scan(content, evidence)
	if len(res.Flagged) != 2 {
		t.Fatalf("want 2 flagged, got %v", res.Flagged)
	}
	if res.Flagged[0] != "zulu" {
		t.Errorf("first-occurrence order: got %v", res.Flagged)
	}
}

func TestLooksLikeIdentifier(t *testing.T) {
	if !looksLikeIdentifier("foo") {
		t.Error("foo should be ident-like")
	}
	if !looksLikeIdentifier("foo.bar") {
		t.Error("foo.bar should be ident-like")
	}
	if looksLikeIdentifier("123") {
		t.Error("pure digits not ident-like")
	}
	if looksLikeIdentifier("...") {
		t.Error("punctuation-only not ident-like")
	}
}

func TestScan_StopwordCaseInsensitive(t *testing.T) {
	s := NewScanner()
	content := "Run `BASH` and `Grep` on this."
	evidence := ""
	res := s.Scan(content, evidence)
	if strings.Contains(res.Annotated, "[?]") {
		t.Errorf("stopwords are case-insensitive; should not flag: %q", res.Annotated)
	}
}

func TestScan_NoSelfReferenceFalsePositive(t *testing.T) {
	// If the model writes "I will define `Helper`" and then proceeds
	// to define Helper in the same response, the identifier still
	// flags as unverified — we DON'T scan the proposed content
	// itself, only external evidence. This is a documented limit;
	// the user can scroll up and see the model is defining it. We
	// pin this behaviour so a refactor doesn't accidentally flip
	// it to fuzzy self-reference acceptance.
	s := NewScanner()
	content := "Define `Helper` like this. The `Helper` function does X."
	evidence := ""
	res := s.Scan(content, evidence)
	if !strings.Contains(res.Annotated, "[?]") {
		t.Errorf("self-reference still flags; if you change this, update the docs: %q", res.Annotated)
	}
}
