package providers

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

// goldPNG is a tiny 1x1 PNG so tests carry real bytes without bloating.
var goldPNG = []byte{
	0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
	0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
}

func TestAnthropicMessages_PlainTextUnchanged(t *testing.T) {
	msgs := []Message{{Role: "user", Content: "hello"}}
	got := anthropicMessages(msgs)
	if len(got) != 1 {
		t.Fatalf("want 1 message, got %d", len(got))
	}
	if got[0].Content != "hello" {
		t.Errorf("plain text should stay as string, got %T %v", got[0].Content, got[0].Content)
	}
}

func TestAnthropicMessages_ImageAttachmentEmitsBlocks(t *testing.T) {
	msgs := []Message{{
		Role:    "user",
		Content: "what is this?",
		Attachments: []Attachment{{
			Kind:      AttachmentImage,
			MediaType: "image/png",
			Data:      goldPNG,
		}},
	}}
	got := anthropicMessages(msgs)
	blocks, ok := got[0].Content.([]anthropicContentBlock)
	if !ok {
		t.Fatalf("attachments should switch to content blocks, got %T", got[0].Content)
	}
	if len(blocks) != 2 {
		t.Fatalf("want image+text = 2 blocks, got %d", len(blocks))
	}
	if blocks[0].Type != "image" || blocks[0].Source == nil {
		t.Errorf("first block should be image with source, got %+v", blocks[0])
	}
	if blocks[0].Source.MediaType != "image/png" {
		t.Errorf("media type lost: %s", blocks[0].Source.MediaType)
	}
	if got, _ := base64.StdEncoding.DecodeString(blocks[0].Source.Data); string(got) != string(goldPNG) {
		t.Errorf("base64 roundtrip failed")
	}
	if blocks[1].Type != "text" || blocks[1].Text != "what is this?" {
		t.Errorf("second block should be text, got %+v", blocks[1])
	}
}

func TestAnthropicMessages_ImageWithoutTextOmitsTextBlock(t *testing.T) {
	msgs := []Message{{
		Role:    "user",
		Content: "",
		Attachments: []Attachment{{
			Kind:      AttachmentImage,
			MediaType: "image/png",
			Data:      goldPNG,
		}},
	}}
	got := anthropicMessages(msgs)
	blocks := got[0].Content.([]anthropicContentBlock)
	if len(blocks) != 1 {
		t.Errorf("empty text should produce image-only block list, got %d blocks", len(blocks))
	}
}

func TestOpenAIMessages_PlainTextUnchanged(t *testing.T) {
	msgs := []Message{{Role: "user", Content: "hello"}}
	got := openAIMessages(msgs)
	if s, ok := got[0].Content.(string); !ok || s != "hello" {
		t.Errorf("plain text should stay string, got %T %v", got[0].Content, got[0].Content)
	}
}

func TestOpenAIMessages_ImageEmitsContentParts(t *testing.T) {
	msgs := []Message{{
		Role:    "user",
		Content: "describe this",
		Attachments: []Attachment{{
			Kind:      AttachmentImage,
			MediaType: "image/png",
			Data:      goldPNG,
		}},
	}}
	got := openAIMessages(msgs)
	parts, ok := got[0].Content.([]openaiContentPart)
	if !ok {
		t.Fatalf("attachments should switch to parts, got %T", got[0].Content)
	}
	if len(parts) != 2 {
		t.Fatalf("want image+text = 2 parts, got %d", len(parts))
	}
	if parts[0].Type != "image_url" || parts[0].ImageURL == nil {
		t.Errorf("first part should be image_url, got %+v", parts[0])
	}
	if !strings.HasPrefix(parts[0].ImageURL.URL, "data:image/png;base64,") {
		t.Errorf("data URL malformed: %s", parts[0].ImageURL.URL)
	}
	if parts[1].Type != "text" || parts[1].Text != "describe this" {
		t.Errorf("second part should be text, got %+v", parts[1])
	}
}

func TestGeminiMessages_ImageEmitsInlineData(t *testing.T) {
	msgs := []Message{{
		Role:    "user",
		Content: "hi",
		Attachments: []Attachment{{
			Kind:      AttachmentImage,
			MediaType: "image/png",
			Data:      goldPNG,
		}},
	}}
	got := geminiMessages(msgs)
	if len(got[0].Parts) != 2 {
		t.Fatalf("want image+text parts, got %d: %+v", len(got[0].Parts), got[0].Parts)
	}
	if got[0].Parts[0].InlineData == nil {
		t.Fatalf("first part missing inline data: %+v", got[0].Parts[0])
	}
	if got[0].Parts[0].InlineData.MimeType != "image/png" {
		t.Errorf("mime type lost: %s", got[0].Parts[0].InlineData.MimeType)
	}
	if got[0].Parts[1].Text != "hi" {
		t.Errorf("text part lost: %s", got[0].Parts[1].Text)
	}
}

func TestGeminiMessages_NoAttachmentsPlainTextSurvives(t *testing.T) {
	msgs := []Message{{Role: "user", Content: "just text"}}
	got := geminiMessages(msgs)
	if len(got[0].Parts) != 1 || got[0].Parts[0].Text != "just text" {
		t.Errorf("plain text path broken: %+v", got[0].Parts)
	}
	if got[0].Parts[0].InlineData != nil {
		t.Errorf("plain text should not emit inlineData")
	}
}

// TestAnthropicMessages_JSONShape catches regressions where the
// JSON marshaling of the new content blocks doesn't match Anthropic's
// expected wire format. We verify keys, not the full doc.
func TestAnthropicMessages_JSONShape(t *testing.T) {
	msgs := []Message{{
		Role: "user",
		Attachments: []Attachment{{
			Kind:      AttachmentImage,
			MediaType: "image/jpeg",
			Data:      []byte{1, 2, 3},
		}},
	}}
	out, err := json.Marshal(anthropicMessages(msgs))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := []string{
		`"type":"image"`,
		`"source":{`,
		`"type":"base64"`,
		`"media_type":"image/jpeg"`,
		`"data":"AQID"`,
	}
	for _, w := range want {
		if !strings.Contains(string(out), w) {
			t.Errorf("missing %q in:\n%s", w, out)
		}
	}
}
