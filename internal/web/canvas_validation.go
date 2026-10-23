package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// decodeCanvasJSON rejects duplicate member names at every depth. Unmarshal
// alone silently keeps the last duplicate, including escaped equivalent names.
func decodeCanvasJSON(data []byte, dst any) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '{' {
		return fmt.Errorf("JSON object required")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	if err := walkCanvasJSON(d); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("exactly one JSON value required")
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	d.UseNumber()
	return d.Decode(dst)
}

// Request names are case-sensitive; encoding/json's struct decoder otherwise
// accepts case-folded aliases and can silently overwrite the logical field.
func decodeCanvasRequest(data []byte, dst any, allowed ...string) error {
	var fields map[string]json.RawMessage
	if err := decodeCanvasJSON(data, &fields); err != nil {
		return err
	}
	for key, value := range fields {
		known := false
		for _, name := range allowed {
			if key == name {
				known = true
				break
			}
		}
		if !known {
			return fmt.Errorf("unknown request field %q", key)
		}
		if len(value) == 0 || value[0] != '"' {
			return fmt.Errorf("request field %q must be a string", key)
		}
	}
	return json.Unmarshal(data, dst)
}

func walkCanvasJSON(d *json.Decoder) error {
	return walkCanvasJSONDepth(d, 0)
}

func walkCanvasJSONDepth(d *json.Decoder, depth int) error {
	if depth > 64 {
		return fmt.Errorf("JSON nesting exceeds 64")
	}
	tok, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok {
				return fmt.Errorf("object member name required")
			}
			if seen[name] {
				return fmt.Errorf("duplicate JSON member %q", name)
			}
			seen[name] = true
			if err := walkCanvasJSONDepth(d, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err := walkCanvasJSONDepth(d, depth+1); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter")
	}
	_, err = d.Token()
	return err
}

func canvasIdentifier(value string) bool {
	if value == "" || len(value) > 256 || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// validateCanvasEvent validates the transport envelope, not renderer-specific
// component catalogs. A catalog renderer must validate its own node schema.
func validateCanvasEvent(line, sessionID, surfaceID string) (string, string, error) {
	var event map[string]json.RawMessage
	if err := decodeCanvasJSON([]byte(line), &event); err != nil {
		return "", "", err
	}
	if len(event) != 1 {
		return "", "", fmt.Errorf("jsonl must contain exactly one A2UI action")
	}
	for action, value := range event {
		switch action {
		case "surfaceUpdate", "dataModelUpdate", "beginRendering", "createSurface", "deleteSurface":
		default:
			return "", "", fmt.Errorf("unknown A2UI action %q", action)
		}
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(value, &payload); err != nil || payload == nil {
			return "", "", fmt.Errorf("action payload must be an object")
		}
		var target string
		if err := json.Unmarshal(payload["surfaceId"], &target); err != nil || !canvasIdentifier(target) {
			return "", "", fmt.Errorf("action payload requires a valid surfaceId")
		}
		if surfaceID != "" && surfaceID != target {
			return "", "", fmt.Errorf("surfaceId disagrees with action payload")
		}
		if raw, ok := payload["sessionId"]; ok {
			var sid string
			if err := json.Unmarshal(raw, &sid); err != nil || sid != sessionID {
				return "", "", fmt.Errorf("sessionId disagrees with action payload")
			}
		}
		return action, target, nil
	}
	return "", "", fmt.Errorf("A2UI action required")
}
