package diagnostic

import (
	"path/filepath"
	"strings"
)

type FileRole struct {
	Path    string   `json:"path"`
	Role    string   `json:"role"`
	Package string   `json:"package"`
	Type    string   `json:"type"`
	Exports []string `json:"exports"`
}

func AnalyzeFile(path string, content string) FileRole {
	fr := FileRole{
		Path: path,
		Type: "unknown",
	}

	fr.Package = extractPackage(content)
	fr.Exports = extractExports(content)
	fr.Type = classifyFileType(path, content)
	fr.Role = describeRole(fr.Type, fr.Package, fr.Exports)

	return fr
}

func extractPackage(content string) string {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "package ") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, "package "))
		}
	}
	return ""
}

func extractExports(content string) []string {
	var exports []string
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if isExportLine(trimmed) {
			name := extractExportName(trimmed)
			if name != "" {
				exports = append(exports, name)
			}
		}
	}
	return exports
}

func isExportLine(line string) bool {
	if !strings.HasPrefix(line, "func ") && !strings.HasPrefix(line, "type ") && !strings.HasPrefix(line, "var ") && !strings.HasPrefix(line, "const ") {
		return false
	}
	if strings.HasPrefix(line, "func (") {
		parts := strings.Split(line, ") ")
		if len(parts) < 2 {
			return false
		}
		afterReceiver := parts[1]
		return len(afterReceiver) > 0 && afterReceiver[0] >= 'A' && afterReceiver[0] <= 'Z'
	}
	rest := line
	switch {
	case strings.HasPrefix(line, "func "):
		rest = strings.TrimPrefix(line, "func ")
	case strings.HasPrefix(line, "type "):
		rest = strings.TrimPrefix(line, "type ")
	case strings.HasPrefix(line, "var "):
		rest = strings.TrimPrefix(line, "var ")
	case strings.HasPrefix(line, "const "):
		rest = strings.TrimPrefix(line, "const ")
	}
	return len(rest) > 0 && rest[0] >= 'A' && rest[0] <= 'Z'
}

func extractExportName(line string) string {
	if strings.HasPrefix(line, "func (") {
		parts := strings.Split(line, ") ")
		if len(parts) < 2 {
			return ""
		}
		afterReceiver := parts[1]
		name := strings.Split(afterReceiver, "(")[0]
		name = strings.Split(name, " ")[0]
		return name
	}

	var rest string
	switch {
	case strings.HasPrefix(line, "func "):
		rest = strings.TrimPrefix(line, "func ")
	case strings.HasPrefix(line, "type "):
		rest = strings.TrimPrefix(line, "type ")
	case strings.HasPrefix(line, "var "):
		rest = strings.TrimPrefix(line, "var ")
	case strings.HasPrefix(line, "const "):
		rest = strings.TrimPrefix(line, "const ")
	default:
		return ""
	}

	name := strings.Split(rest, " ")[0]
	name = strings.Split(name, "(")[0]
	name = strings.Split(name, "{")[0]
	return name
}

func classifyFileType(path string, content string) string {
	base := strings.ToLower(filepath.Base(path))

	switch {
	case isConfigFile(base):
		return "config"
	case strings.HasSuffix(base, "_test.go"):
		return "test"
	case strings.Contains(content, "func main()"):
		return "entry"
	case hasInterface(content):
		return "interface"
	case hasTestMethod(content) && strings.HasSuffix(base, ".go"):
		return "test"
	case hasImplementation(content):
		return "implementation"
	case hasHelperFunctions(content):
		return "utility"
	}

	return "unknown"
}

func isConfigFile(name string) bool {
	configFiles := []string{
		"go.mod", "go.sum", "go.work",
		".toml", ".yaml", ".yml", ".json", ".ini", ".cfg",
	}
	for _, cf := range configFiles {
		if name == cf || strings.HasSuffix(name, cf) {
			return true
		}
	}
	return false
}

func hasInterface(content string) bool {
	return strings.Contains(content, " interface {") || strings.Contains(content, " interface{")
}

func hasTestMethod(content string) bool {
	return strings.Contains(content, "func Test")
}

func hasImplementation(content string) bool {
	hasNewFunc := strings.Contains(content, "func New")
	hasMethod := strings.Contains(content, "func (")
	return hasNewFunc || hasMethod
}

func hasHelperFunctions(content string) bool {
	lines := strings.Split(content, "\n")
	funcCount := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "func ") {
			funcCount++
		}
	}
	return funcCount > 0 && len(extractExports(content)) == 0
}

func describeRole(fileType string, pkg string, exports []string) string {
	switch fileType {
	case "entry":
		return "entry point"
	case "test":
		return "test file"
	case "interface":
		return "contract definition"
	case "implementation":
		return "implementation"
	case "config":
		return "configuration"
	case "utility":
		return "utility helpers"
	default:
		if pkg != "" {
			return "package " + pkg
		}
		return "unknown"
	}
}
