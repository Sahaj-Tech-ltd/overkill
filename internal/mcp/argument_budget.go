package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"unicode/utf8"
)

// Leave room for the JSON-RPC method, request ID, tool name and envelope.
const maxMCPArgumentBytes = maxMCPLineBytes - 1024

func decodeCallArguments(args json.RawMessage) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(args))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("mcp: valid JSON arguments required")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, fmt.Errorf("mcp: one JSON argument value required")
	}
	return value, nil
}

// encodedJSONSize matches encoding/json's HTML-escaped string encoding without
// allocating a serialized copy. All additions fail before crossing the budget.
func encodedJSONSize(value any, limit int) (int, error) {
	used := 0
	add := func(amount int) error {
		if amount < 0 || amount > limit-used {
			return fmt.Errorf("mcp: transformed arguments exceed byte limit")
		}
		used += amount
		return nil
	}
	var walk func(any) error
	walk = func(value any) error {
		switch value := value.(type) {
		case nil:
			return add(4)
		case bool:
			if value {
				return add(4)
			}
			return add(5)
		case json.Number:
			return add(len(value))
		case string:
			if err := add(2); err != nil {
				return err
			}
			for len(value) > 0 {
				r, size := utf8.DecodeRuneInString(value)
				amount := size
				switch {
				case r == '"' || r == '\\':
					amount = 2
				case r == '\b' || r == '\f' || r == '\n' || r == '\r' || r == '\t':
					amount = 2
				case r < 0x20 || r == '<' || r == '>' || r == '&' || r == '\u2028' || r == '\u2029' || (r == utf8.RuneError && size == 1):
					amount = 6
				}
				if err := add(amount); err != nil {
					return err
				}
				value = value[size:]
			}
			return nil
		case []any:
			if err := add(2); err != nil {
				return err
			}
			for index, item := range value {
				if index > 0 {
					if err := add(1); err != nil {
						return err
					}
				}
				if err := walk(item); err != nil {
					return err
				}
			}
			return nil
		case map[string]any:
			if err := add(2); err != nil {
				return err
			}
			index := 0
			for key, item := range value {
				if index > 0 {
					if err := add(1); err != nil {
						return err
					}
				}
				index++
				if err := walk(key); err != nil {
					return err
				}
				if err := add(1); err != nil {
					return err
				}
				if err := walk(item); err != nil {
					return err
				}
			}
			return nil
		default:
			return fmt.Errorf("mcp: unsupported JSON argument value")
		}
	}
	if err := walk(value); err != nil {
		return 0, err
	}
	return used, nil
}
