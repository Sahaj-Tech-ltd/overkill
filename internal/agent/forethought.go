package agent

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

type RiskLevel int

const (
	RiskLow RiskLevel = iota
	RiskMedium
	RiskHigh
)

type ImpactAssessment struct {
	ToolName      string
	AffectedPaths []string
	RiskLevel     RiskLevel
	Reasoning     string
	Protected     bool
}

var protectedPaths = []string{
	"go.mod", "go.sum", "go.work",
	".github/", ".git/",
	"Makefile", "Dockerfile", "docker-compose.yml",
	"LICENSE", "LICENSE-MIT", "LICENSE-APACHE",
}

type Forethinker struct{}

func NewForethinker() *Forethinker {
	return &Forethinker{}
}

var writeCommandPattern = regexp.MustCompile(`(?i)\b(rm|rmdir|mv|cp|chmod|chown|ln|mkdir|touch|tee|install|dd|truncate|split)\b`)
var redirectPattern = regexp.MustCompile(`[12]?>{1,2}\s*(\S+)`)
var appendPattern = regexp.MustCompile(`>>\s*(\S+)`)
var sedInPlacePattern = regexp.MustCompile(`(?i)\bsed\b.*\s-i\s`)
var pathExtractor = regexp.MustCompile(`(?:[\w./-]+/)?[\w.-]+\.[\w]+`)
var flagPattern = regexp.MustCompile(`^-{1,2}`)
var commandBuiltinPattern = regexp.MustCompile(`^(sudo|do|if|then|else|fi|while|do|done|for|in|case|esac|echo|printf|cat|ls|grep|find|head|tail|sort|uniq|wc|awk|sed|curl|wget|git|go|npm|pip|docker|make|cd|pwd|export|source|set|unset|env|which|whoami|id|uname|date|sleep|true|false|yes|no|test|xargs|tr|cut|paste|join|fmt|pr|expand|unexpand|fold|column|ts|tac|rev|nl|basename|dirname|realpath|readlink|mknod|mount|umount|mkfs|fsck|df|du|lsblk|blkid|fdisk|parted|losetup|tune2fs|dumpe2fs|debugfs|badblocks|smartctl|hdparm|sdparm|lshw|lspci|lsusb|dmidecode|biosdecode|vpddecode|ownership|selinux|getenforce|setenforce|chcon|restorecon|auditctl|ausearch|aureport|autrace|auditd|sealert|fixfiles|semodule|semanage|seinfo|sesearch|findmnt|flock|timeout|ionice|nice|renice|nohup|screen|tmux|strace|ltrace|gdb|perf|valgrind|lsof|fuser|lscpu|numactl|taskset|chrt|ionice)$`)

func isProtectedPath(p string) bool {
	for _, pp := range protectedPaths {
		// Path prefix match: path starts with the protected entry.
		if strings.HasPrefix(p, pp) {
			return true
		}
		// Path component match: the protected entry appears as a
		// full path component (preceded by "/"). This prevents
		// substring false positives like "my.github/workflows/"
		// matching the ".github/" protected entry.
		if strings.Contains(p, "/"+pp) {
			return true
		}
	}
	return false
}

func extractPathsFromCommand(cmd string) []string {
	var paths []string
	seen := make(map[string]bool)

	redirectMatches := redirectPattern.FindAllStringSubmatch(cmd, -1)
	for _, m := range redirectMatches {
		if len(m) > 1 {
			p := strings.Trim(m[1], `'"`)
			if !seen[p] && !flagPattern.MatchString(p) {
				paths = append(paths, p)
				seen[p] = true
			}
		}
	}

	appendMatches := appendPattern.FindAllStringSubmatch(cmd, -1)
	for _, m := range appendMatches {
		if len(m) > 1 {
			p := strings.Trim(m[1], `'"`)
			if !seen[p] && !flagPattern.MatchString(p) {
				paths = append(paths, p)
				seen[p] = true
			}
		}
	}

	tokens := strings.Fields(cmd)
	isWriteCmd := len(tokens) > 0 && writeCommandPattern.MatchString(tokens[0])

	for i, tok := range tokens {
		if i == 0 {
			continue
		}
		clean := strings.Trim(tok, `'"`)
		if flagPattern.MatchString(clean) {
			continue
		}
		if isWriteCmd || strings.Contains(clean, "/") || strings.Contains(clean, ".") {
			if !seen[clean] && !commandBuiltinPattern.MatchString(clean) && clean != "and" && clean != "or" {
				paths = append(paths, clean)
				seen[clean] = true
			}
		}
	}

	return paths
}

func hasWriteOps(cmd string) bool {
	if writeCommandPattern.MatchString(cmd) {
		return true
	}
	if strings.Contains(cmd, ">") || strings.Contains(cmd, ">>") {
		return true
	}
	if sedInPlacePattern.MatchString(cmd) {
		return true
	}
	return false
}

func (f *Forethinker) Assess(toolName string, input json.RawMessage) *ImpactAssessment {
	assessment := &ImpactAssessment{
		ToolName: toolName,
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(input, &raw); err != nil {
		_ = json.Unmarshal(input, &raw)
	}

	switch toolName {
	case "shell":
		f.assessShell(assessment, raw)
	case "fs":
		assessment.RiskLevel = RiskMedium
		assessment.Reasoning = "File system operations are inherently write operations"
		if raw != nil {
			if path, ok := raw["path"].(string); ok {
				assessment.AffectedPaths = []string{path}
				if isProtectedPath(path) {
					assessment.RiskLevel = RiskHigh
					assessment.Protected = true
					assessment.Reasoning = fmt.Sprintf("File system operation on protected path: %s", path)
				}
			}
		}
	case "grep", "web", "git":
		assessment.RiskLevel = RiskLow
		assessment.Reasoning = fmt.Sprintf("Tool %q is read-only", toolName)
	default:
		assessment.RiskLevel = RiskMedium
		assessment.Reasoning = fmt.Sprintf("Unknown tool %q, defaulting to medium risk", toolName)
	}

	if raw != nil {
		if cmd, ok := raw["command"].(string); ok {
			if strings.Contains(cmd, "go.mod") || strings.Contains(cmd, "go.sum") {
				assessment.RiskLevel = RiskHigh
				assessment.Protected = true
				assessment.Reasoning = fmt.Sprintf("Command targets protected Go module file in: %s", cmd)
			}
		}
		if path, ok := raw["path"].(string); ok {
			if strings.Contains(path, "go.mod") || strings.Contains(path, "go.sum") {
				assessment.RiskLevel = RiskHigh
				assessment.Protected = true
				assessment.Reasoning = fmt.Sprintf("Operation targets protected Go module file: %s", path)
			}
		}
	}

	return assessment
}

func (f *Forethinker) assessShell(assessment *ImpactAssessment, raw map[string]interface{}) {
	if raw == nil {
		assessment.RiskLevel = RiskLow
		assessment.Reasoning = "Empty shell input"
		return
	}

	cmd, _ := raw["command"].(string)
	if cmd == "" {
		assessment.RiskLevel = RiskLow
		assessment.Reasoning = "Empty shell command"
		return
	}

	if !hasWriteOps(cmd) {
		assessment.RiskLevel = RiskLow
		assessment.Reasoning = fmt.Sprintf("Read-only command: %s", cmd)
		return
	}

	paths := extractPathsFromCommand(cmd)
	assessment.AffectedPaths = paths

	for _, p := range paths {
		if isProtectedPath(p) {
			assessment.RiskLevel = RiskHigh
			assessment.Protected = true
			assessment.Reasoning = fmt.Sprintf("Command modifies protected path %s in: %s", p, cmd)
			return
		}
	}

	switch len(paths) {
	case 0:
		assessment.RiskLevel = RiskMedium
		assessment.Reasoning = fmt.Sprintf("Write operation detected but no specific paths extracted: %s", cmd)
	case 1, 2:
		assessment.RiskLevel = RiskMedium
		assessment.Reasoning = fmt.Sprintf("Command modifies %d path(s): %s", len(paths), strings.Join(paths, ", "))
	default:
		assessment.RiskLevel = RiskHigh
		assessment.Reasoning = fmt.Sprintf("Command modifies %d paths: %s", len(paths), strings.Join(paths, ", "))
	}
}
