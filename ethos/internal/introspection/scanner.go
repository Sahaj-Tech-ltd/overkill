package introspection

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ScanResult is the deterministic output of WalkAndSummarize. Used by /init
// to seed ~/.overkill/introspection/CODEBASE.md without requiring an LLM call.
type ScanResult struct {
	Root          string
	Languages     []string
	Packages      []PackageSummary
	GoExports     []GoExport
	TSExports     []TSExport
	Conventions   []string
	TopLevelTree  []string
	IgnoredCount  int
	ScannedFiles  int
	GitignoreRoot string
}

type PackageSummary struct {
	Name      string // e.g. "go.mod" / "package.json" basename's project
	Manifest  string // path to manifest file
	Stack     string // "go" / "node" / "python" / "rust"
	NameField string // module name / package name field if found
}

type GoExport struct {
	Package string
	File    string
	Kind    string // "func" / "type" / "var" / "const"
	Name    string
}

type TSExport struct {
	File string
	Name string
}

// gitignore patterns we skip wholesale even if no .gitignore exists.
var defaultIgnoreDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	"dist":         true,
	"build":        true,
	".next":        true,
	"target":       true, // rust
	"__pycache__":  true,
	".venv":        true,
	"venv":         true,
	".idea":        true,
	".vscode":      true,
}

// WalkAndSummarize walks dir respecting common ignore patterns and returns a
// structural summary suitable for rendering as CODEBASE.md.
func WalkAndSummarize(dir string) (*ScanResult, error) {
	res := &ScanResult{Root: dir}

	gi := loadGitignore(filepath.Join(dir, ".gitignore"))
	res.GitignoreRoot = dir

	// Top-level tree (depth 1).
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			name := e.Name()
			if strings.HasPrefix(name, ".") && !e.IsDir() {
				continue
			}
			suffix := ""
			if e.IsDir() {
				suffix = "/"
			}
			res.TopLevelTree = append(res.TopLevelTree, name+suffix)
		}
		sort.Strings(res.TopLevelTree)
	}

	langs := map[string]bool{}
	goExports := []GoExport{}
	tsExports := []TSExport{}

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return nil // skip unreadable
		}
		rel, _ := filepath.Rel(dir, path)
		if rel == "." {
			return nil
		}
		base := d.Name()

		if d.IsDir() {
			if defaultIgnoreDirs[base] {
				res.IgnoredCount++
				return filepath.SkipDir
			}
			if gi != nil && gi.match(rel, true) {
				res.IgnoredCount++
				return filepath.SkipDir
			}
			return nil
		}

		// Manifests.
		switch base {
		case "go.mod":
			res.Packages = append(res.Packages, scanGoMod(path))
			langs["Go"] = true
		case "package.json":
			res.Packages = append(res.Packages, scanPackageJSON(path))
			langs["JavaScript/TypeScript"] = true
		case "pyproject.toml", "setup.py", "requirements.txt":
			res.Packages = append(res.Packages, PackageSummary{
				Manifest: path, Stack: "python", Name: filepath.Base(filepath.Dir(path)),
			})
			langs["Python"] = true
		case "Cargo.toml":
			res.Packages = append(res.Packages, PackageSummary{
				Manifest: path, Stack: "rust", Name: filepath.Base(filepath.Dir(path)),
			})
			langs["Rust"] = true
		}

		ext := strings.ToLower(filepath.Ext(base))
		switch ext {
		case ".go":
			res.ScannedFiles++
			langs["Go"] = true
			if exps := scanGoFile(path); len(exps) > 0 {
				goExports = append(goExports, exps...)
			}
		case ".ts", ".tsx":
			res.ScannedFiles++
			langs["TypeScript"] = true
			if exps := scanTSFile(path); len(exps) > 0 {
				tsExports = append(tsExports, exps...)
			}
		case ".js", ".jsx":
			res.ScannedFiles++
			langs["JavaScript"] = true
		case ".py":
			res.ScannedFiles++
			langs["Python"] = true
		case ".rs":
			res.ScannedFiles++
			langs["Rust"] = true
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("introspection: walk: %w", err)
	}

	for l := range langs {
		res.Languages = append(res.Languages, l)
	}
	sort.Strings(res.Languages)
	res.GoExports = goExports
	res.TSExports = tsExports
	res.Conventions = inferConventions(dir)

	return res, nil
}

// scanGoMod reads the module name out of go.mod (first `module` directive).
func scanGoMod(path string) PackageSummary {
	ps := PackageSummary{Manifest: path, Stack: "go", Name: filepath.Base(filepath.Dir(path))}
	f, err := os.Open(path)
	if err != nil {
		return ps
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "module ") {
			ps.NameField = strings.TrimSpace(strings.TrimPrefix(line, "module"))
			break
		}
	}
	return ps
}

// scanPackageJSON reads the "name" field out of package.json without pulling
// in encoding/json overhead for large monorepos.
func scanPackageJSON(path string) PackageSummary {
	ps := PackageSummary{Manifest: path, Stack: "node", Name: filepath.Base(filepath.Dir(path))}
	data, err := os.ReadFile(path)
	if err != nil {
		return ps
	}
	re := regexp.MustCompile(`"name"\s*:\s*"([^"]+)"`)
	if m := re.FindStringSubmatch(string(data)); len(m) >= 2 {
		ps.NameField = m[1]
	}
	return ps
}

// scanGoFile uses the standard go/parser to extract exported declarations.
func scanGoFile(path string) []GoExport {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil
	}
	pkg := file.Name.Name
	var out []GoExport
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Name.IsExported() {
				kind := "func"
				if d.Recv != nil {
					kind = "method"
				}
				out = append(out, GoExport{Package: pkg, File: path, Kind: kind, Name: d.Name.Name})
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if s.Name.IsExported() {
						out = append(out, GoExport{Package: pkg, File: path, Kind: "type", Name: s.Name.Name})
					}
				case *ast.ValueSpec:
					for _, n := range s.Names {
						if n.IsExported() {
							kind := "var"
							if d.Tok == token.CONST {
								kind = "const"
							}
							out = append(out, GoExport{Package: pkg, File: path, Kind: kind, Name: n.Name})
						}
					}
				}
			}
		}
	}
	return out
}

// scanTSFile is a heuristic line-grep for `export <kind> <Name>`. Avoids a TS
// parser dependency. Misses some patterns; that's intentional per scope.
var tsExportRe = regexp.MustCompile(`^\s*export\s+(?:default\s+)?(?:async\s+)?(?:function|class|interface|type|const|let|var|enum)\s+([A-Za-z_$][\w$]*)`)

func scanTSFile(path string) []TSExport {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []TSExport
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		if m := tsExportRe.FindStringSubmatch(sc.Text()); len(m) >= 2 {
			out = append(out, TSExport{File: path, Name: m[1]})
		}
	}
	return out
}

// inferConventions returns a few heuristic conventions detected in the repo
// root (presence of formatting / lint config, README sections, etc.).
func inferConventions(root string) []string {
	var out []string
	check := func(name, label string) {
		if _, err := os.Stat(filepath.Join(root, name)); err == nil {
			out = append(out, label)
		}
	}
	check(".editorconfig", "Uses .editorconfig for whitespace/indent")
	check(".gitignore", "Has .gitignore")
	check("Makefile", "Make-driven build")
	check(".golangci.yml", "Lint: golangci-lint")
	check(".eslintrc.json", "Lint: eslint")
	check(".prettierrc", "Format: prettier")
	check("ruff.toml", "Lint: ruff")
	check("pyproject.toml", "Python project (pyproject)")
	check("Dockerfile", "Containerized")
	check("docker-compose.yml", "docker-compose used")
	check("CONTRIBUTING.md", "CONTRIBUTING.md present")
	check("CODEOWNERS", "CODEOWNERS present")
	return out
}

// gitignore is a minimal matcher — supports leading-slash anchored patterns,
// trailing-slash dir patterns, and basic wildcards. Sufficient for /init.
type gitignore struct {
	patterns []string
}

func loadGitignore(path string) *gitignore {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	g := &gitignore{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		g.patterns = append(g.patterns, line)
	}
	return g
}

func (g *gitignore) match(rel string, isDir bool) bool {
	for _, p := range g.patterns {
		pat := strings.TrimPrefix(p, "/")
		dirOnly := strings.HasSuffix(pat, "/")
		pat = strings.TrimSuffix(pat, "/")
		if dirOnly && !isDir {
			continue
		}
		if matched, _ := filepath.Match(pat, rel); matched {
			return true
		}
		if matched, _ := filepath.Match(pat, filepath.Base(rel)); matched {
			return true
		}
	}
	return false
}

// RenderCodebaseMarkdown turns a ScanResult into the CODEBASE.md body.
func RenderCodebaseMarkdown(res *ScanResult) string {
	if res == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Codebase overview\n\n")
	fmt.Fprintf(&b, "_Generated by `ethos /init`. Deterministic structural scan; not an LLM summary._\n\n")
	fmt.Fprintf(&b, "Root: `%s`\n\n", res.Root)

	if len(res.Languages) > 0 {
		fmt.Fprintf(&b, "## Languages detected\n\n")
		for _, l := range res.Languages {
			fmt.Fprintf(&b, "- %s\n", l)
		}
		fmt.Fprintln(&b)
	}

	if len(res.TopLevelTree) > 0 {
		fmt.Fprintf(&b, "## Top-level layout\n\n```\n")
		for _, n := range res.TopLevelTree {
			fmt.Fprintf(&b, "%s\n", n)
		}
		fmt.Fprintf(&b, "```\n\n")
	}

	if len(res.Packages) > 0 {
		fmt.Fprintf(&b, "## Packages / manifests\n\n")
		for _, p := range res.Packages {
			rel := p.Manifest
			if r, err := filepath.Rel(res.Root, p.Manifest); err == nil {
				rel = r
			}
			name := p.NameField
			if name == "" {
				name = p.Name
			}
			fmt.Fprintf(&b, "- **%s** (%s) — `%s`\n", name, p.Stack, rel)
		}
		fmt.Fprintln(&b)
	}

	if len(res.GoExports) > 0 {
		fmt.Fprintf(&b, "## Public Go interfaces\n\n")
		// Group by package.
		byPkg := map[string][]GoExport{}
		for _, e := range res.GoExports {
			byPkg[e.Package] = append(byPkg[e.Package], e)
		}
		pkgs := make([]string, 0, len(byPkg))
		for p := range byPkg {
			pkgs = append(pkgs, p)
		}
		sort.Strings(pkgs)
		for _, p := range pkgs {
			fmt.Fprintf(&b, "### %s\n\n", p)
			items := byPkg[p]
			sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
			for _, it := range items {
				rel := it.File
				if r, err := filepath.Rel(res.Root, it.File); err == nil {
					rel = r
				}
				fmt.Fprintf(&b, "- `%s %s` (%s)\n", it.Kind, it.Name, rel)
			}
			fmt.Fprintln(&b)
		}
	}

	if len(res.TSExports) > 0 {
		fmt.Fprintf(&b, "## Public TypeScript exports\n\n")
		byFile := map[string][]string{}
		for _, e := range res.TSExports {
			rel := e.File
			if r, err := filepath.Rel(res.Root, e.File); err == nil {
				rel = r
			}
			byFile[rel] = append(byFile[rel], e.Name)
		}
		files := make([]string, 0, len(byFile))
		for f := range byFile {
			files = append(files, f)
		}
		sort.Strings(files)
		for _, f := range files {
			names := byFile[f]
			sort.Strings(names)
			fmt.Fprintf(&b, "- `%s`: %s\n", f, strings.Join(names, ", "))
		}
		fmt.Fprintln(&b)
	}

	if len(res.Conventions) > 0 {
		fmt.Fprintf(&b, "## Detected conventions\n\n")
		for _, c := range res.Conventions {
			fmt.Fprintf(&b, "- %s\n", c)
		}
		fmt.Fprintln(&b)
	}

	fmt.Fprintf(&b, "---\n\n")
	fmt.Fprintf(&b, "Scanned %d files. Skipped %d ignored directories.\n",
		res.ScannedFiles, res.IgnoredCount)
	return b.String()
}
