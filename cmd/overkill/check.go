package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var (
	checkFix  bool
	checkFull bool
)

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Run pre-push quality checks",
	Long: `Run all quality checks before pushing. By default runs fast checks (~10s).
Use --full for comprehensive lint + security scanning (~60s).
Use --fix to auto-correct formatting issues.`,
	RunE: runCheck,
}

func init() {
	checkCmd.Flags().BoolVar(&checkFix, "fix", false, "auto-fix formatting issues")
	checkCmd.Flags().BoolVar(&checkFull, "full", false, "run comprehensive lint + security scans")
	rootCmd.AddCommand(checkCmd)
}

type checkResult struct {
	name   string
	passed bool
	output string
}

func runCheck(cmd *cobra.Command, args []string) error {
	root, _ := findRepoRoot()
	var results []checkResult
	failures := 0

	// ── 1. Secret scan ──
	r := checkSecretScan(root)
	results = append(results, r)

	// ── 2. Go format ──
	r = checkGoFmt(root)
	results = append(results, r)

	// ── 3. Go vet ──
	r = checkGoVet(root)
	results = append(results, r)

	// ── 4. Go vulns ──
	r = checkGoVulns(root)
	results = append(results, r)

	// ── 5. TypeScript ──
	r = checkTypeScript(root)
	results = append(results, r)

	// ── 6. npm audit ──
	r = checkNpmAudit(root)
	results = append(results, r)

	// ── 7. golangci-lint (full only) ──
	if checkFull {
		r = checkGolangciLint(root)
		results = append(results, r)
	}

	// ── 8. gosec via golangci-lint (full only) ──
	if checkFull {
		r = checkGosec(root)
		results = append(results, r)
	}

	// Print results
	fmt.Println("━━━ Overkill Pre-Push Check ━━━")
	for _, r := range results {
		marker := "✓"
		if !r.passed {
			marker = "✗"
			failures++
		}
		fmt.Printf("  [%s] %s\n", marker, r.name)
		if r.output != "" {
			for _, line := range strings.Split(r.output, "\n") {
				if line != "" {
					fmt.Printf("       %s\n", line)
				}
			}
		}
	}

	if failures > 0 {
		fmt.Printf("\n  %d check(s) failed.\n", failures)
		if !checkFix {
			fmt.Println("  Run with --fix to auto-correct formatting.")
		}
		os.Exit(1)
	}
	fmt.Println("\n  All checks passed. Safe to push.")
	return nil
}

func shellRun(name string, dir string, args ...string) (string, bool) {
	c := exec.Command(args[0], args[1:]...)
	c.Dir = dir
	out, err := c.CombinedOutput()
	return string(out), err == nil
}

func checkSecretScan(root string) checkResult {
	// Simple grep for common secret patterns (exclude node_modules, deprecated)
	patterns := []string{
		`ghp_[A-Za-z0-9]{36,}`,
		`sk-[A-Za-z0-9]{32,}`,
		`xai-[A-Za-z0-9]{32,}`,
	}
	dirsToSkip := []string{"node_modules", "deprecated", ".git", "inspiration"}
	for _, pattern := range patterns {
		args := []string{"-r", "-l", "-E", pattern, "--include=*.go", "--include=*.ts", "--include=*.tsx", "--include=*.toml", "--include=*.yaml", "--include=*.env"}
		for _, skip := range dirsToSkip {
			args = append(args, "--exclude-dir="+skip)
		}
		args = append(args, root)
		c := exec.Command("grep", args...)
		out, _ := c.CombinedOutput()
		// Filter out test files (false positives from fixtures)
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		var realHits []string
		for _, line := range lines {
			if line != "" && !strings.Contains(line, "_test.go") && !strings.Contains(line, ".test.ts") {
				realHits = append(realHits, line)
			}
		}
		if len(realHits) > 0 {
			return checkResult{"Secret scan", false, fmt.Sprintf("suspicious pattern found:\n%s", strings.Join(realHits, "\n"))}
		}
	}
	return checkResult{"Secret scan", true, ""}
}

func checkGoFmt(root string) checkResult {
	out, ok := shellRun("gofmt", root, "gofmt", "-l", ".")
	if !ok {
		return checkResult{"Go format (gofmt)", false, out}
	}
	unformatted := strings.TrimSpace(out)
	if unformatted == "" {
		return checkResult{"Go format (gofmt)", true, ""}
	}
	if checkFix {
		shellRun("gofmt -w", root, "gofmt", "-w", ".")
		return checkResult{"Go format (gofmt)", true, "auto-fixed"}
	}
	lines := strings.Split(unformatted, "\n")
	return checkResult{"Go format (gofmt)", false, fmt.Sprintf("%d unformatted files:\n%s", len(lines), strings.Join(lines[:min(5, len(lines))], "\n"))}
}

func checkGoVet(root string) checkResult {
	out, ok := shellRun("go vet", root, "go", "vet", "./...")
	if !ok {
		return checkResult{"Go vet", false, out}
	}
	return checkResult{"Go vet", true, ""}
}

func checkGoVulns(root string) checkResult {
	_, err := exec.LookPath("govulncheck")
	if err != nil {
		return checkResult{"Go vulns (govulncheck)", true, "skipped (not installed)"}
	}
	out, ok := shellRun("govulncheck", root, "govulncheck", "./...")
	if !ok && strings.Contains(out, "Vulnerability") {
		return checkResult{"Go vulns (govulncheck)", false, out}
	}
	return checkResult{"Go vulns (govulncheck)", true, ""}
}

func checkTypeScript(root string) checkResult {
	tuiDir := filepath.Join(root, "tui")
	if _, err := os.Stat(filepath.Join(tuiDir, "package.json")); err != nil {
		return checkResult{"TypeScript (prettier+tsc)", true, "skipped (no tui/)"}
	}

	// Prettier
	if checkFix {
		shellRun("prettier", tuiDir, "npx", "prettier", "--write", "--loglevel", "silent", "src/**/*.{ts,tsx}")
	} else {
		_, ok := shellRun("prettier", tuiDir, "npx", "prettier", "--check", "--loglevel", "silent", "src/**/*.{ts,tsx}")
		if !ok {
			return checkResult{"TypeScript (prettier+tsc)", false, "prettier: run with --fix"}
		}
	}

	// tsc
	out, ok := shellRun("tsc", tuiDir, "npx", "tsc", "--noEmit")
	if !ok {
		return checkResult{"TypeScript (prettier+tsc)", false, out}
	}
	return checkResult{"TypeScript (prettier+tsc)", true, ""}
}

func checkNpmAudit(root string) checkResult {
	tuiDir := filepath.Join(root, "tui")
	if _, err := os.Stat(filepath.Join(tuiDir, "package.json")); err != nil {
		return checkResult{"npm audit", true, "skipped (no tui/)"}
	}
	out, _ := shellRun("npm audit", tuiDir, "npm", "audit", "--audit-level=high")
	if strings.Contains(out, "vulnerabilities") && !strings.Contains(out, "0 vulnerabilities") {
		return checkResult{"npm audit", false, out}
	}
	return checkResult{"npm audit", true, ""}
}

func checkGolangciLint(root string) checkResult {
	_, err := exec.LookPath("golangci-lint")
	if err != nil {
		return checkResult{"Go lint (golangci-lint)", true, "skipped (not installed)"}
	}
	out, ok := shellRun("golangci-lint", root, "golangci-lint", "run", "--timeout=60s", "./cmd/...", "./internal/...")
	if !ok {
		return checkResult{"Go lint (golangci-lint)", false, out}
	}
	return checkResult{"Go lint (golangci-lint)", true, ""}
}

func checkGosec(root string) checkResult {
	_, err := exec.LookPath("golangci-lint")
	if err != nil {
		return checkResult{"Go security (gosec)", true, "skipped (not installed)"}
	}
	out, ok := shellRun("golangci-lint gosec", root, "golangci-lint", "run", "--timeout=60s", "--disable-all", "--enable=gosec", "./cmd/...", "./internal/...")
	if !ok {
		return checkResult{"Go security (gosec)", false, out}
	}
	return checkResult{"Go security (gosec)", true, ""}
}
