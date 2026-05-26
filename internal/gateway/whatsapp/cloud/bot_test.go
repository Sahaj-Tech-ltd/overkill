package cloud

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVerifySignature_ValidPasses(t *testing.T) {
	secret := "app-secret-xyz"
	body := []byte(`{"object":"whatsapp_business_account"}`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	header := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if !verifySignature(body, secret, header) {
		t.Error("valid signature should pass")
	}
}

func TestVerifySignature_WrongSecretFails(t *testing.T) {
	body := []byte(`{}`)
	mac := hmac.New(sha256.New, []byte("right-secret"))
	mac.Write(body)
	header := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if verifySignature(body, "wrong-secret", header) {
		t.Error("wrong secret must fail")
	}
}

func TestVerifySignature_MissingPrefixFails(t *testing.T) {
	body := []byte(`{}`)
	mac := hmac.New(sha256.New, []byte("s"))
	mac.Write(body)
	headerNoPrefix := hex.EncodeToString(mac.Sum(nil))

	if verifySignature(body, "s", headerNoPrefix) {
		t.Error("header without 'sha256=' prefix must fail")
	}
}

func TestVerifySignature_TamperedBodyFails(t *testing.T) {
	body := []byte(`{"a":1}`)
	mac := hmac.New(sha256.New, []byte("s"))
	mac.Write(body)
	header := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	tampered := []byte(`{"a":2}`)
	if verifySignature(tampered, "s", header) {
		t.Error("body must not verify against a signature for a different body")
	}
}

func TestVerifySignature_MalformedHexFails(t *testing.T) {
	if verifySignature([]byte("body"), "s", "sha256=not-hex-xyz") {
		t.Error("invalid hex should fail cleanly, not panic")
	}
}

func TestVerifySignature_EmptyHeaderFails(t *testing.T) {
	if verifySignature([]byte("body"), "s", "") {
		t.Error("empty header must fail")
	}
}

func TestHandleVerify_MatchEchoesChallenge(t *testing.T) {
	bot := &Bot{VerifyToken: "secret-token"}
	req := httptest.NewRequest(http.MethodGet, "/webhook?hub.mode=subscribe&hub.verify_token=secret-token&hub.challenge=abc123", nil)
	rec := httptest.NewRecorder()
	bot.handleVerify(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: %d", rec.Code)
	}
	if rec.Body.String() != "abc123" {
		t.Errorf("body: %q want abc123", rec.Body.String())
	}
}

func TestHandleVerify_WrongTokenForbidden(t *testing.T) {
	bot := &Bot{VerifyToken: "real"}
	req := httptest.NewRequest(http.MethodGet, "/webhook?hub.mode=subscribe&hub.verify_token=fake&hub.challenge=x", nil)
	rec := httptest.NewRecorder()
	bot.handleVerify(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("wrong token must 403, got %d", rec.Code)
	}
}

func TestHandleVerify_WrongModeBadRequest(t *testing.T) {
	bot := &Bot{VerifyToken: "real"}
	req := httptest.NewRequest(http.MethodGet, "/webhook?hub.mode=unsubscribe&hub.verify_token=real", nil)
	rec := httptest.NewRecorder()
	bot.handleVerify(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("wrong mode must 400, got %d", rec.Code)
	}
}

func TestHandleMessage_RejectsUnsignedBody(t *testing.T) {
	bot := &Bot{AppSecret: "s"}
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(`{"x":1}`))
	rec := httptest.NewRecorder()
	bot.handleMessage(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("unsigned body must 401, got %d", rec.Code)
	}
}

func TestHandleMessage_AcceptsSignedBody(t *testing.T) {
	body := `{"object":"whatsapp_business_account","entry":[]}`
	mac := hmac.New(sha256.New, []byte("s"))
	mac.Write([]byte(body))
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	bot := &Bot{AppSecret: "s"}
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sig)
	rec := httptest.NewRecorder()
	bot.handleMessage(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("signed body must 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestPayloadParse_ExtractsTextMessage(t *testing.T) {
	// Pin the schema we depend on. If Meta changes the shape in a
	// breaking way (renames fields) this test surfaces it before
	// production traffic does.
	raw := `{
	  "object": "whatsapp_business_account",
	  "entry": [{
	    "id": "biz-acct",
	    "changes": [{
	      "field": "messages",
	      "value": {
	        "messaging_product": "whatsapp",
	        "metadata": {"display_phone_number":"+1","phone_number_id":"123"},
	        "contacts": [{"wa_id":"14155551234","profile":{"name":"Test User"}}],
	        "messages": [{
	          "from":"14155551234",
	          "id":"wamid.HBgL...",
	          "timestamp":"1700000000",
	          "type":"text",
	          "text":{"body":"hello bot"}
	        }]
	      }
	    }]
	  }]
	}`
	var p webhookPayload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(p.Entry) != 1 || len(p.Entry[0].Changes) != 1 {
		t.Fatal("entry/changes shape wrong")
	}
	msgs := p.Entry[0].Changes[0].Value.Messages
	if len(msgs) != 1 {
		t.Fatalf("want 1 msg, got %d", len(msgs))
	}
	if msgs[0].Text.Body != "hello bot" {
		t.Errorf("body: %q", msgs[0].Text.Body)
	}
	if msgs[0].From != "14155551234" {
		t.Errorf("from: %q", msgs[0].From)
	}
}

func TestPayloadParse_ImageMessageHasMediaID(t *testing.T) {
	raw := `{
	  "object": "x", "entry":[{"id":"a","changes":[{"field":"m","value":{
	    "messages":[{
	      "from":"1234","id":"x","timestamp":"0","type":"image",
	      "image":{"id":"media-abc","mime_type":"image/jpeg","caption":"look"}
	    }]
	  }}]}]
	}`
	var p webhookPayload
	_ = json.Unmarshal([]byte(raw), &p)
	msg := p.Entry[0].Changes[0].Value.Messages[0]
	if msg.Image == nil {
		t.Fatal("image not parsed")
	}
	if msg.Image.ID != "media-abc" {
		t.Errorf("media id: %q", msg.Image.ID)
	}
	if msg.Image.Caption != "look" {
		t.Errorf("caption: %q", msg.Image.Caption)
	}
}

func TestNewBot_StripsPlusFromAllowList(t *testing.T) {
	bot := NewBot("pn", "tok", "sec", "vtok", "127.0.0.1:0",
		[]string{"+14155551234", "14155555678", "  +1415  "}, nil)
	if !bot.AllowedFrom["14155551234"] {
		t.Error("leading + should be stripped")
	}
	if !bot.AllowedFrom["14155555678"] {
		t.Error("plain number should be kept")
	}
	if !bot.AllowedFrom["1415"] {
		t.Error("whitespace + + should be stripped")
	}
}

func TestValidate_RequiresAllSecrets(t *testing.T) {
	tests := []struct {
		name string
		bot  *Bot
	}{
		{"missing phone id", &Bot{AccessToken: "t", AppSecret: "s", VerifyToken: "v", Listen: "l"}},
		{"missing access token", &Bot{PhoneNumberID: "p", AppSecret: "s", VerifyToken: "v", Listen: "l"}},
		{"missing app secret", &Bot{PhoneNumberID: "p", AccessToken: "t", VerifyToken: "v", Listen: "l"}},
		{"missing verify token", &Bot{PhoneNumberID: "p", AccessToken: "t", AppSecret: "s", Listen: "l"}},
		{"missing listen", &Bot{PhoneNumberID: "p", AccessToken: "t", AppSecret: "s", VerifyToken: "v"}},
	}
	for _, c := range tests {
		t.Run(c.name, func(t *testing.T) {
			if err := c.bot.validate(); err == nil {
				t.Error("expected error for missing field")
			}
		})
	}
}

func TestGraphURL_DefaultsToProductionEndpoint(t *testing.T) {
	bot := &Bot{}
	if got := bot.graphURL(); !strings.HasPrefix(got, "https://graph.facebook.com") {
		t.Errorf("default graph URL wrong: %s", got)
	}

	bot.GraphURL = "http://localhost:9999/v18.0"
	if got := bot.graphURL(); got != "http://localhost:9999/v18.0" {
		t.Errorf("override ignored: %s", got)
	}
}

func TestBot_Name(t *testing.T) {
	if got := (&Bot{}).Name(); got != "whatsapp-cloud" {
		t.Errorf("name: %q", got)
	}
}
