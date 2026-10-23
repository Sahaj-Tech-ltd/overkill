package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
)

func canonicalLocalPath(path string) (string, error) {
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("mcp: absolute local path required")
	}
	current := path
	var missing []string
	for {
		if _, err := os.Lstat(current); err == nil {
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", fmt.Errorf("mcp: path has unresolved symlinks")
			}
			for index := len(missing) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missing[index])
			}
			return resolved, nil
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("mcp: path cannot be inspected")
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("mcp: path has no resolvable parent")
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}
func normalizePathPolicy(server config.MCPServer) (config.MCPServer, error) {
	if len(server.AllowedPathPrefixes) == 0 {
		if len(server.PathArgumentBindings) > 0 || server.PathWorkspace != "" {
			return server, fmt.Errorf("mcp: path bindings require allowed_path_prefixes")
		}
		return server, nil
	}
	if server.PathWorkspace == "" {
		return server, fmt.Errorf("mcp: allowed_path_prefixes requires path_workspace and explicit path_argument_bindings")
	}
	workspace, err := canonicalLocalPath(server.PathWorkspace)
	if err != nil {
		return server, err
	}
	info, err := os.Stat(workspace)
	if err != nil || !info.IsDir() {
		return server, fmt.Errorf("mcp: path_workspace must be an existing local directory")
	}
	server.PathWorkspace = workspace
	prefixes := make([]string, 0, len(server.AllowedPathPrefixes))
	for _, prefix := range server.AllowedPathPrefixes {
		root, err := canonicalLocalPath(prefix)
		if err != nil {
			return server, err
		}
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			return server, fmt.Errorf("mcp: allowed path roots must be existing local directories")
		}
		prefixes = append(prefixes, root)
	}
	server.AllowedPathPrefixes = prefixes
	bindings := make(map[string][]string, len(server.PathArgumentBindings))
	for tool, pointers := range server.PathArgumentBindings {
		if tool == "" || len(pointers) > 128 {
			return server, fmt.Errorf("mcp: invalid path binding")
		}
		for _, pointer := range pointers {
			if _, err := pointerParts(pointer); err != nil {
				return server, err
			}
		}
		bindings[tool] = append([]string{}, pointers...)
	}
	server.PathArgumentBindings = bindings
	return server, nil
}
func pointerParts(pointer string) ([]string, error) {
	if len(pointer) > 512 || !strings.HasPrefix(pointer, "/") {
		return nil, fmt.Errorf("mcp: path bindings require non-root RFC6901 JSON pointers")
	}
	parts := strings.Split(pointer[1:], "/")
	for index, part := range parts {
		for offset := 0; offset < len(part); offset++ {
			if part[offset] == '~' {
				if offset+1 >= len(part) || (part[offset+1] != '0' && part[offset+1] != '1') {
					return nil, fmt.Errorf("mcp: invalid RFC6901 escape")
				}
				offset++
			}
		}
		parts[index] = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
	}
	return parts, nil
}
func pointerValue(root any, parts []string) (any, func(any), error) {
	current := root
	for index, part := range parts {
		last := index == len(parts)-1
		switch node := current.(type) {
		case map[string]any:
			value, ok := node[part]
			if !ok {
				return nil, nil, fmt.Errorf("mcp: bound path argument is missing")
			}
			if last {
				return value, func(v any) { node[part] = v }, nil
			}
			current = value
		case []any:
			number, err := strconv.Atoi(part)
			if err != nil || number < 0 || number >= len(node) || strconv.Itoa(number) != part {
				return nil, nil, fmt.Errorf("mcp: invalid path array pointer")
			}
			if last {
				return node[number], func(v any) { node[number] = v }, nil
			}
			current = node[number]
		default:
			return nil, nil, fmt.Errorf("mcp: unsupported path argument shape")
		}
	}
	return nil, nil, fmt.Errorf("mcp: empty path argument pointer")
}
func schemaSupportsPointer(schema map[string]any, parts []string) bool {
	current := schema
	for _, part := range parts {
		switch current["type"] {
		case "object":
			properties, ok := current["properties"].(map[string]any)
			if !ok {
				return false
			}
			current, ok = properties[part].(map[string]any)
			if !ok {
				return false
			}
		case "array":
			number, err := strconv.Atoi(part)
			if err != nil || number < 0 || strconv.Itoa(number) != part {
				return false
			}
			var ok bool
			current, ok = current["items"].(map[string]any)
			if !ok {
				return false
			}
		default:
			return false
		}
	}
	if current["type"] == "string" {
		return true
	}
	if current["type"] == "array" {
		items, ok := current["items"].(map[string]any)
		return ok && items["type"] == "string"
	}
	return false
}
func bindCallPaths(server config.MCPServer, definition Tool, args json.RawMessage) (json.RawMessage, []string, error) {
	limit := maxMCPArgumentBytes
	if server.MaxBytesPerCall > 0 && server.MaxBytesPerCall < limit {
		limit = server.MaxBytesPerCall
	}
	return bindCallPathsWithLimit(server, definition, args, limit)
}

func bindCallPathsWithLimit(server config.MCPServer, definition Tool, args json.RawMessage, limit int) (json.RawMessage, []string, error) {
	if len(args) > limit {
		return nil, nil, fmt.Errorf("mcp: arguments exceed byte limit")
	}

	pointers, ok := server.PathArgumentBindings[definition.Name]
	if !ok {
		return nil, nil, fmt.Errorf("mcp: path-limited tool needs an explicit path_argument_bindings entry (empty list only for tools with no path inputs)")
	}
	var schema map[string]any
	if json.Unmarshal(definition.InputSchema, &schema) != nil || schema["type"] != "object" {
		return nil, nil, fmt.Errorf("mcp: path-limited tool requires a current object input schema")
	}
	values, err := decodeCallArguments(args)
	if err != nil {
		return nil, nil, err
	}
	usedBytes, err := encodedJSONSize(values, limit)
	if err != nil {
		return nil, nil, err
	}
	if _, ok := values.(map[string]any); !ok {
		return nil, nil, fmt.Errorf("mcp: object arguments required")
	}
	paths := make([]string, 0)
	uniquePaths := make(map[string]bool)
	canonicalize := func(value string) (string, error) {
		oldSize, err := encodedJSONSize(value, limit)
		if err != nil {
			return "", err
		}
		if value == "" || strings.ContainsRune(value, 0) {
			return "", fmt.Errorf("mcp: nonempty local path required")
		}
		if !filepath.IsAbs(value) {
			value = filepath.Join(server.PathWorkspace, value)
		}
		path, err := canonicalLocalPath(value)
		if err != nil {
			return "", err
		}
		newSize, err := encodedJSONSize(path, limit)
		if err != nil {
			return "", err
		}
		if newSize-oldSize > limit-usedBytes {
			return "", fmt.Errorf("mcp: transformed arguments exceed byte limit")
		}
		usedBytes += newSize - oldSize
		if !uniquePaths[path] {
			paths = append(paths, path)
			uniquePaths[path] = true
		}
		return path, nil
	}
	for _, pointer := range pointers {
		parts, err := pointerParts(pointer)
		if err != nil {
			return nil, nil, err
		}
		if !schemaSupportsPointer(schema, parts) {
			return nil, nil, fmt.Errorf("mcp: path binding does not match the current supported string/string-array schema")
		}
		value, set, err := pointerValue(values, parts)
		if err != nil {
			return nil, nil, err
		}
		switch input := value.(type) {
		case string:
			path, err := canonicalize(input)
			if err != nil {
				return nil, nil, err
			}
			set(path)
		case []any:
			for index, item := range input {
				text, ok := item.(string)
				if !ok {
					return nil, nil, fmt.Errorf("mcp: bound path arrays must contain only strings")
				}
				path, err := canonicalize(text)
				if err != nil {
					return nil, nil, err
				}
				input[index] = path
			}
			set(input)
		default:
			return nil, nil, fmt.Errorf("mcp: bound paths must be strings or string arrays")
		}
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return nil, nil, err
	}
	if len(encoded) > limit {
		return nil, nil, fmt.Errorf("mcp: transformed arguments exceed byte limit")
	}
	return encoded, paths, nil
}
