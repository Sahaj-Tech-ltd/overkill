package security

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// ShellSyntaxFindings statically inspects shell syntax without expanding words or
// executing anything. It supplements the legacy text patterns (including user
// patterns); it is not a shell sandbox. Callers accepting commands must reject
// parse errors. Generic text scanners may ignore them because they also scan prose.
func ShellSyntaxFindings(input string) ([]Finding, error) {
	return shellSyntaxFindings(input, 0)
}

const maxShellSyntaxBytes = 256 * 1024

func shellSyntaxFindings(input string, depth int) ([]Finding, error) {
	if len(input) > maxShellSyntaxBytes || depth > 8 {
		return nil, fmt.Errorf("shell syntax inspection limit exceeded")
	}
	tree, err := syntax.NewParser().Parse(strings.NewReader(input), "")
	if err != nil {
		return nil, fmt.Errorf("cannot parse shell command: %w", err)
	}
	var out []Finding
	add := func(description string, level ThreatLevel) {
		out = append(out, Finding{Type: "dangerous_command", Level: level, Description: description, Match: input, Confidence: 0.95})
	}
	visited := 0
	pipelineSeen := make(map[*syntax.BinaryCmd]bool)
	var inspectionErr error
	syntax.Walk(tree, func(n syntax.Node) bool {
		if n == nil {
			return false
		}
		visited++
		if visited > 32768 {
			inspectionErr = fmt.Errorf("shell syntax node limit exceeded")
			return false
		}

		switch n := n.(type) {
		case *syntax.Redirect:
			switch n.Op {
			case syntax.RdrOut, syntax.AppOut, syntax.RdrInOut, syntax.RdrClob, syntax.RdrAll, syntax.AppAll:
				target := shellWord(n.Word)
				for _, prefix := range []string{"/dev/sd", "/dev/hd", "/dev/vd", "/dev/nvme", "/dev/disk", "/dev/rdisk", "/dev/mmcblk"} {
					if strings.HasPrefix(target, prefix) {
						add("direct device write", ThreatCritical)
						break
					}
				}
			}
		case *syntax.CallExpr:
			args := shellArgs(n)
			if len(args) == 0 {
				break
			}
			name := filepath.Base(args[0])
			args = args[1:]
			switch name {
			case "rm":
				recursive, force, options := false, false, true
				var targets []string
				for _, arg := range args {
					if options && arg == "--" {
						options = false
						continue
					}
					if options && strings.HasPrefix(arg, "--") {
						recursive = recursive || arg == "--recursive"
						force = force || arg == "--force"
						continue
					}
					if options && strings.HasPrefix(arg, "-") && arg != "-" {
						recursive = recursive || strings.ContainsAny(arg[1:], "rR")
						force = force || strings.Contains(arg[1:], "f")
						continue
					}
					targets = append(targets, arg)
				}
				if recursive && force {
					for _, target := range targets {
						if strings.HasPrefix(target, "/") || strings.HasPrefix(target, "~") || strings.HasPrefix(target, "$") || target == "." || strings.HasPrefix(target, "./") || target == ".." || strings.HasPrefix(target, "../") || strings.ContainsAny(target, "*?") {
							add("recursive force delete (root/home/current dir)", ThreatCritical)
							break
						}
					}
				}
			case "dd":
				for _, arg := range args {
					if strings.HasPrefix(arg, "of=/dev/") {
						add("raw disk write", ThreatCritical)
						break
					}
				}
			case "sh", "bash", "dash", "zsh", "ksh", "fish", "ash":
				for i, arg := range args {
					if strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && strings.Contains(arg, "c") && i+1 < len(args) {
						nested, nestedErr := shellSyntaxFindings(args[i+1], depth+1)
						if nestedErr != nil {
							add("uninspectable nested shell command", ThreatHigh)
						} else {
							out = append(out, nested...)
						}
						break
					}
				}
			}
		case *syntax.BinaryCmd:
			if pipelineSeen[n] || n.Op != syntax.Pipe && n.Op != syntax.PipeAll {
				break
			}
			var commands [][]string
			collectPipeline(n, &commands, pipelineSeen)
			source, decoder := false, false
			for _, args := range commands {
				if len(args) == 0 {
					continue
				}
				name := filepath.Base(args[0])
				if isShell(name) && (source || decoder) {
					add("download or decoded data piped to shell", ThreatHigh)
				}
				source = source || name == "curl" || name == "wget"
				decoder = decoder || isDecoder(args)
			}
		}
		return true
	})
	return out, inspectionErr
}

func isShell(name string) bool {
	switch name {
	case "sh", "bash", "dash", "zsh", "ksh", "fish", "ash":
		return true
	}
	return false
}
func isDecoder(args []string) bool {
	name := filepath.Base(args[0])
	for _, arg := range args[1:] {
		if name == "base64" && (arg == "--decode" || strings.HasPrefix(arg, "-") && strings.ContainsAny(arg, "dD")) || name == "xxd" && strings.HasPrefix(arg, "-") && strings.Contains(arg, "r") || name == "printf" && arg == "%b" {
			return true
		}
	}
	return false
}
func collectPipeline(stmt syntax.Node, out *[][]string, seen map[*syntax.BinaryCmd]bool) {
	switch n := stmt.(type) {
	case *syntax.BinaryCmd:
		if n.Op == syntax.Pipe || n.Op == syntax.PipeAll {
			seen[n] = true
			collectPipeline(n.X, out, seen)
			collectPipeline(n.Y, out, seen)
		}
	case *syntax.Stmt:
		collectPipeline(n.Cmd, out, seen)
	case *syntax.CallExpr:
		// Command-substitution output can be fed through echo/printf to the next
		// pipeline stage. Inspect syntax nodes, never quoted literal text.
		for _, word := range n.Args {
			syntax.Walk(word, func(child syntax.Node) bool {
				if call, ok := child.(*syntax.CallExpr); ok {
					*out = append(*out, shellArgs(call))
				}
				return true
			})
		}
		*out = append(*out, shellArgs(n))
	}
}
func shellArgs(call *syntax.CallExpr) []string {
	var args []string
	for _, w := range call.Args {
		args = append(args, shellWord(w))
	}
	// These wrappers do not turn their following command into data. Skip their
	// options/assignments conservatively; every resulting command is still parsed.
	for len(args) > 1 {
		name := filepath.Base(args[0])
		if name != "command" && name != "exec" && name != "env" && name != "sudo" {
			break
		}
		args = args[1:]
		for len(args) > 0 {
			arg := args[0]
			if arg == "--" {
				args = args[1:]
				break
			}
			if name == "env" && strings.Contains(arg, "=") {
				args = args[1:]
				continue
			}
			if !strings.HasPrefix(arg, "-") {
				break
			}
			// Options which consume a separate value must not hide the command.
			takesValue := name == "env" && (arg == "-u" || arg == "--unset" || arg == "-C" || arg == "--chdir") || name == "exec" && arg == "-a" || name == "sudo" && (arg == "-u" || arg == "--user" || arg == "-g" || arg == "--group" || arg == "-h" || arg == "--host" || arg == "-p" || arg == "--prompt" || arg == "-C" || arg == "-D" || arg == "-R" || arg == "-T" || arg == "-r" || arg == "-t")
			args = args[1:]
			if takesValue && len(args) > 0 {
				args = args[1:]
			}
		}
	}
	return args
}
func shellWord(word *syntax.Word) string {
	var b strings.Builder
	for _, part := range word.Parts {
		switch p := part.(type) {
		case *syntax.Lit:
			b.WriteString(unescapeShellLiteral(p.Value))
		case *syntax.SglQuoted:
			if p.Dollar {
				b.WriteString(unescapeDollarQuoted(p.Value))
			} else {
				b.WriteString(p.Value)
			}
		case *syntax.DblQuoted:
			b.WriteString(shellWord(&syntax.Word{Parts: p.Parts}))
		default:
			b.WriteString("$dynamic") // Never evaluate substitutions or expansions.
		}
	}
	return b.String()
}

func unescapeShellLiteral(value string) string {
	var b strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] == '\\' && i+1 < len(value) {
			i++
			if value[i] == '\n' {
				continue
			}
		}
		b.WriteByte(value[i])
	}
	return b.String()
}

// ANSI-C quoting is syntax, not command execution. Decode its literal escapes so
// $'\x72\x6d' has the same classification as rm.
func unescapeDollarQuoted(value string) string {
	var b strings.Builder
	for len(value) > 0 {
		if strings.HasPrefix(value, `\e`) || strings.HasPrefix(value, `\E`) {
			b.WriteByte(27)
			value = value[2:]
			continue
		}
		r, _, tail, err := strconv.UnquoteChar(value, '\'')
		if err != nil {
			return "$dynamic"
		}
		b.WriteRune(r)
		value = tail
	}
	return b.String()
}
