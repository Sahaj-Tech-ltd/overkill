package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/pelletier/go-toml/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// CheckResult records the actual command and scope behind a verification.
// An absent runner/script is unavailable, never evidence of a passing build.
type CheckResult struct {
	Language  string   `json:"language"`
	Kind      string   `json:"kind"`
	Directory string   `json:"directory"`
	Command   []string `json:"command,omitempty"`
	Status    string   `json:"status"`
	Output    string   `json:"output,omitempty"`
}

func skipDirectory(name string) bool {
	return strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules" || name == "__pycache__"
}

func verificationPlan(ctx context.Context, workDir string) ([]CheckResult, error) {
	var checks []CheckResult
	dirs := map[string]map[string]bool{}
	files, err := projectFiles(ctx, workDir)
	if err != nil {
		return nil, err
	}
	for _, path := range files {
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		name := filepath.Base(path)
		if name != "go.mod" && name != "package.json" && name != "pyproject.toml" && name != "setup.py" && name != "requirements.txt" && name != "pytest.ini" {
			continue
		}
		dir := filepath.Dir(path)
		if dirs[dir] == nil {
			dirs[dir] = map[string]bool{}
		}
		dirs[dir][name] = true
	}
	roots := make([]string, 0, len(dirs))
	for dir := range dirs {
		roots = append(roots, dir)
	}
	sort.Strings(roots)
	add := func(lang, kind, dir string, args ...string) {
		checks = append(checks, CheckResult{Language: lang, Kind: kind, Directory: dir, Command: args})
	}
	for _, dir := range roots {
		files := dirs[dir]
		if files["go.mod"] {
			add("go", "build", dir, "go", "build", "./...")
			add("go", "test", dir, "go", "test", "./...", "-count=1", "-timeout", "30s")
		}
		if files["package.json"] {
			var pkg struct {
				Scripts map[string]string `json:"scripts"`
			}
			data, err := os.ReadFile(filepath.Join(dir, "package.json"))
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(data, &pkg); err != nil {
				return nil, fmt.Errorf("audit: invalid package.json in %s: %w", dir, err)
			}
			lang := "javascript"
			if _, err := os.Stat(filepath.Join(dir, "tsconfig.json")); err == nil {
				lang = "typescript"
			}
			if strings.TrimSpace(pkg.Scripts["build"]) != "" {
				add(lang, "build", dir, "npm", "run", "build")
			} else if lang == "typescript" {
				add(lang, "build", dir, "npm", "exec", "--offline", "--", "tsc", "--noEmit")
			} else {
				add(lang, "build", dir)
			}
			if strings.TrimSpace(pkg.Scripts["test"]) != "" {
				add(lang, "test", dir, "npm", "test")
			} else {
				add(lang, "test", dir)
			}
		}
		if files["pyproject.toml"] || files["setup.py"] || files["requirements.txt"] || files["pytest.ini"] {
			add("python", "build", dir, "python3", "-m", "compileall", "-q", ".")
			pytest := files["pytest.ini"]
			if files["pyproject.toml"] {
				data, err := os.ReadFile(filepath.Join(dir, "pyproject.toml"))
				if err != nil {
					return nil, err
				}
				var project map[string]any
				if err := toml.Unmarshal(data, &project); err != nil {
					return nil, fmt.Errorf("audit: invalid pyproject.toml: %w", err)
				}
				pytest = pytest || declaresPytest(project)
			}
			if files["requirements.txt"] {
				data, _ := os.ReadFile(filepath.Join(dir, "requirements.txt"))
				for _, line := range strings.Split(string(data), "\n") {
					line = strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
					if pytestDependency(line) {
						pytest = true
					}
				}
			}

			if pytest {
				add("python", "test", dir, "python3", "-m", "pytest")
			} else {
				add("python", "test", dir, "python3", "-m", "unittest", "discover", "-v")
			}
		}
	}
	if len(checks) > 128 {
		return nil, fmt.Errorf("audit: verification plan exceeds 128 checks; narrow project scope")
	}
	return checks, nil
}

func (a *Auditor) runVerification(ctx context.Context, r *Report) {
	plan, err := verificationPlan(ctx, a.WorkDir)
	if err != nil {
		r.Passed = false
		r.Findings = append(r.Findings, Finding{Severity: "fail", Claim: "verification plan", Actual: err.Error()})
		return
	}
	r.BuildOK, r.TestsOK = true, true
	if len(plan) == 0 {
		r.BuildOK, r.TestsOK, r.Passed = false, false, false
		r.Findings = append(r.Findings, Finding{Severity: "warn", Claim: "language verification", Actual: "no supported Go, Node, or Python project manifest found; build/test unverified"})
		return
	}
	for _, check := range plan {
		if len(check.Command) == 0 {
			check.Status = "unavailable"
			check.Output = "project does not declare this verification script"
		} else {
			output, err := runCmd(ctx, check.Directory, check.Command[0], check.Command[1:]...)
			check.Output = firstLines(output, 15)
			if err != nil {
				check.Status = "failed"
				check.Output = fmt.Sprintf("%v\n%s", err, check.Output)
			} else {
				check.Status = "passed"
				if check.Language == "python" && check.Kind == "test" && strings.Contains(output, "Ran 0 tests") {
					check.Status = "unavailable"
					check.Output = "unittest discovered no tests"
				}
			}
		}
		if check.Status != "passed" {
			r.Passed = false
			if check.Kind == "build" {
				r.BuildOK = false
			} else {
				r.TestsOK = false
			}
			r.Findings = append(r.Findings, Finding{Severity: "fail", Claim: check.Language + " " + check.Kind, Expected: strings.Join(check.Command, " "), Actual: check.Status, Evidence: check.Output})
		}
		r.Checks = append(r.Checks, check)
	}
}

func pytestDependency(s string) bool {
	s = strings.TrimSpace(strings.ToLower(s))
	return s == "pytest" || strings.HasPrefix(s, "pytest[") || strings.HasPrefix(s, "pytest ") || strings.HasPrefix(s, "pytest=") || strings.HasPrefix(s, "pytest>") || strings.HasPrefix(s, "pytest<") || strings.HasPrefix(s, "pytest~") || strings.HasPrefix(s, "pytest!")
}
func declaresPytest(v any) bool {
	switch value := v.(type) {
	case map[string]any:
		for key, item := range value {
			if key == "pytest" || pytestDependency(key) || declaresPytest(item) {
				return true
			}
		}
	case []any:
		for _, item := range value {
			if declaresPytest(item) {
				return true
			}
		}
	case string:
		return pytestDependency(value)
	}
	return false
}
