package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ResolvedImport represents a single import with its resolved definition files.
type ResolvedImport struct {
	Path         string   `json:"path"`
	LocalName    string   `json:"local_name"`
	IsStdlib     bool     `json:"is_stdlib"`
	DefFiles     []string `json:"definition_files,omitempty"`
	UsedSymbols  []string `json:"used_symbols,omitempty"`
}

// LocalSymbol represents a symbol defined in the target file.
type LocalSymbol struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"` // function, struct, interface, const, var, type
	Line     int    `json:"line"`
}

// DefFile represents a definition file with a reason for inclusion.
type DefFile struct {
	Path            string   `json:"path"`
	Reason          string   `json:"reason"`
	RelevantSymbols []string `json:"relevant_symbols,omitempty"`
}

// TestFile represents a discovered test file.
type TestFile struct {
	Path       string `json:"path"`
	Confidence string `json:"confidence"` // "direct", "related_dependency"
}

// ContextResult is the structured JSON output of the resolver.
type ContextResult struct {
	TargetFile    string          `json:"target_file"`
	Package       string          `json:"package,omitempty"`
	Language      string          `json:"language,omitempty"`
	Imports       []ResolvedImport `json:"imports,omitempty"`
	LocalSymbols  []LocalSymbol   `json:"local_symbols,omitempty"`
	DefFiles      []DefFile       `json:"definition_files,omitempty"`
	TestFiles     []TestFile      `json:"test_files,omitempty"`
	SiblingFiles  []string        `json:"sibling_files,omitempty"`
	ProjectRoot   string          `json:"project_root"`
	Note          string          `json:"note,omitempty"`
	ElapsedMs     int64           `json:"elapsed_ms"`
}

// ContextResolverTool resolves the full context needed for editing a file.
// Uses Go's standard library (go/parser, go/token) for import extraction,
// AST-based symbol usage tracking, and filepath.WalkDir for discovering
// definition and test files. Zero external dependencies, completes in <2ms.
type ContextResolverTool struct {
	cacheMu sync.RWMutex
	pkgCache map[string]*cachedProject
}

type cachedProject struct {
	projectDir string
	pkgMap     map[string]*pkgInfo
	allGoFiles []string
	testFiles  []string
	grabbedAt  time.Time
}

type pkgInfo struct {
	dir     string
	name    string
	files   map[string]*fileInfo
}

type fileInfo struct {
	path    string
	imports []string
}

// importInfo tracks a parsed import with its effective local name.
type importInfo struct {
	path      string
	localName string
}

func NewContextResolverTool() *ContextResolverTool {
	return &ContextResolverTool{
		pkgCache: make(map[string]*cachedProject),
	}
}

func (t *ContextResolverTool) Name() string { return "context_resolver" }

func (t *ContextResolverTool) Description() string {
	return "Resolve the full context needed to edit a file. " +
		"Scans imports, discovers definition files, finds related test files. " +
		"Use ONCE per subagent spawn target and pass the returned " +
		"definition_files and test_files as ctx_files to spawn_subagent. " +
		"Returns structured JSON in <2ms — zero LLM calls."
}

func (t *ContextResolverTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"target_file": {
				"type": "string",
				"description": "Path to the file being edited (relative to project root or absolute)"
			},
			"project_root": {
				"type": "string",
				"description": "Optional: project root directory. Defaults to CWD."
			},
			"max_def_files": {
				"type": "integer",
				"description": "Maximum definition files to return (default: 10)"
			}
		},
		"required": ["target_file"]
	}`)
}

func (t *ContextResolverTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		TargetFile  string `json:"target_file"`
		ProjectRoot string `json:"project_root"`
		MaxDefFiles int    `json:"max_def_files"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("failed to parse args: %w", err)
	}
	if params.TargetFile == "" {
		return "", fmt.Errorf("target_file is required")
	}
	if params.MaxDefFiles <= 0 {
		params.MaxDefFiles = 10
	}

	start := time.Now()

	targetPath := params.TargetFile
	projectRoot := params.ProjectRoot

	if !filepath.IsAbs(targetPath) {
		// Resolve relative target_file against project_root first, or CWD if not set
		if projectRoot != "" {
			targetPath = filepath.Join(projectRoot, targetPath)
		} else {
			cwd, err := os.Getwd()
			if err != nil {
				return "", fmt.Errorf("failed to get CWD: %w", err)
			}
			projectRoot = cwd
			targetPath = filepath.Join(cwd, targetPath)
		}
	} else {
		if projectRoot == "" {
			projectRoot = filepath.Dir(targetPath)
			dir := projectRoot
			for dir != "/" {
				if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
					projectRoot = dir
					break
				}
				dir = filepath.Dir(dir)
			}
			// Fallback: try non-Go project root markers (Gemfile, Cargo.toml, etc.)
			if projectRoot == filepath.Dir(targetPath) {
				if pr := detectProjectRoot(targetPath); pr != "" {
					projectRoot = pr
				}
			}
		}
	}

	relTarget, err := filepath.Rel(projectRoot, targetPath)
	if err != nil {
		relTarget = targetPath
	}

	ext := filepath.Ext(targetPath)
	lang := detectLanguage(ext)
	moduleName := readModuleName(projectRoot)
	proj := t.getOrBuildIndex(projectRoot)

	fset := token.NewFileSet()
	af, parseErr := parser.ParseFile(fset, targetPath, nil, parser.ParseComments)
	if parseErr != nil {
		// Non-Go file — try gotreesitter for import/symbol extraction
		result := ContextResult{
			TargetFile:  relTarget,
			Language:    lang,
			ProjectRoot: projectRoot,
			ElapsedMs:   time.Since(start).Milliseconds(),
		}
		// Try tree-sitter based extraction for non-Go files
		// Wrapped in safeExtractWithGotreesitter to recover panics from
		// the CGo-free tree-sitter library (e.g. nil grammar references)
		if gs := safeExtractWithGotreesitter(targetPath, projectRoot); gs != nil {
			result.Language = gs.Language
			result.Imports = gs.Imports
			result.LocalSymbols = gs.LocalSymbols
			result.Package = gs.Package

			// Discover test files for this language
			if tests := findTestFiles(projectRoot, gs.Language); len(tests) > 0 {
				result.TestFiles = tests
			}

			// Discover sibling files (language-agnostic)
			result.SiblingFiles = findSiblingFiles(projectRoot, targetPath)
		} else if lang == "unknown" {
			result.Note = "unsupported file type — context_resolver supports Go, Python, JavaScript, TypeScript, Ruby, Rust, Java, Kotlin, C#, Swift, C, C++"
		} else if lang != "" {
			result.Note = fmt.Sprintf("could not parse %s file — tree-sitter grammar unavailable or parse failed", lang)
		}
		return formatResult(result)
}

	result := ContextResult{
		TargetFile:  relTarget,
		Language:    lang,
		ProjectRoot: projectRoot,
	}

	if af.Name != nil {
		result.Package = af.Name.Name
	}

	// ── Step 1: Extract imports with aliases ──
	var importList []importInfo
	importSet := make(map[string]bool)

	for _, imp := range af.Imports {
		path := strings.Trim(imp.Path.Value, "\"")
		if importSet[path] {
			continue
		}
		importSet[path] = true

		info := importInfo{path: path}
		if imp.Name != nil {
			info.localName = imp.Name.Name
		} else {
			parts := strings.Split(path, "/")
			info.localName = parts[len(parts)-1]
		}
		importList = append(importList, info)
	}

	// ── Step 2: Extract usage of imported symbols from the AST ──
	usageMap := extractUsedSymbols(af, importList)

	// ── Step 3: Build result imports with symbol usage ──
	for _, imp := range importList {
		ri := ResolvedImport{
			Path:     imp.path,
			LocalName: imp.localName,
			IsStdlib: isStdLibImport(imp.path, moduleName, proj),
		}

		if symbols, ok := usageMap[imp.localName]; ok && len(symbols) > 0 {
			ri.UsedSymbols = symbols
		}

		if !ri.IsStdlib {
			ri.DefFiles = findDefFiles(proj, imp.path, 3)
		}

		result.Imports = append(result.Imports, ri)
	}

	// ── Step 4: Extract local symbols ──
	result.LocalSymbols = extractLocalSymbols(fset, af)

	// ── Step 5: Build definition file list with used-symbol context ──
	defFileSeen := make(map[string]bool)
	for _, imp := range result.Imports {
		if len(imp.DefFiles) == 0 {
			continue
		}
		symbolStr := ""
		if len(imp.UsedSymbols) > 0 {
			symbolStr = fmt.Sprintf(", used: %s", strings.Join(imp.UsedSymbols, ", "))
		}
		for _, df := range imp.DefFiles {
			rel, _ := filepath.Rel(projectRoot, df)
			if !defFileSeen[rel] {
				defFileSeen[rel] = true
				reason := fmt.Sprintf("import %q%s", imp.Path, symbolStr)
				result.DefFiles = append(result.DefFiles, DefFile{
					Path:            rel,
					Reason:          reason,
					RelevantSymbols: imp.UsedSymbols,
				})
			}
		}
	}

	if len(result.DefFiles) > params.MaxDefFiles {
		result.DefFiles = result.DefFiles[:params.MaxDefFiles]
	}

	// ── Step 6: Find test files ──
	targetDir := filepath.Dir(relTarget)
	for _, tf := range proj.testFiles {
		relTf, _ := filepath.Rel(projectRoot, tf)
		if strings.HasPrefix(relTf, targetDir) ||
			(targetDir == "." && !strings.ContainsRune(relTf, filepath.Separator)) {
			result.TestFiles = append(result.TestFiles, TestFile{
				Path:       relTf,
				Confidence: "direct",
			})
		}
	}
	for _, imp := range result.Imports {
		if imp.IsStdlib {
			continue
		}
		// Strip module prefix to get relative directory; handle root package
		base := filepath.Base(projectRoot)
		var impRel string
		if strings.HasPrefix(imp.Path, base+"/") {
			impRel = imp.Path[len(base)+1:]
		} else if imp.Path == base {
			impRel = ""
		} else {
			impRel = imp.Path
		}
		for _, tf := range proj.testFiles {
			relTf, _ := filepath.Rel(projectRoot, tf)
			if strings.HasPrefix(relTf, impRel) &&
				(len(relTf) == len(impRel) || relTf[len(impRel)] == '/') {
				dup := false
				for _, existing := range result.TestFiles {
					if existing.Path == relTf {
						dup = true
						break
					}
				}
				if !dup {
					result.TestFiles = append(result.TestFiles, TestFile{
						Path:       relTf,
						Confidence: "related_dependency",
					})
				}
			}
		}
	}

	// ── Step 7: Find sibling files ──
	targetDirAbs := filepath.Dir(targetPath)
	if pi, ok := proj.pkgMap[targetDirAbs]; ok {
		for f := range pi.files {
			rel, _ := filepath.Rel(projectRoot, f)
			if rel != relTarget {
				result.SiblingFiles = append(result.SiblingFiles, rel)
			}
		}
	}

	result.ElapsedMs = time.Since(start).Milliseconds()
	return formatResult(result)
}

// safeExtractWithGotreesitter wraps extractWithGotreesitter with a panic
// recovery so a crash in the tree-sitter CGo-free library doesn't take
// down the whole tool. Returns nil on panic or parse failure.
func safeExtractWithGotreesitter(targetPath, projectRoot string) (ctx *extractedContext) {
	defer func() {
		if r := recover(); r != nil {
			ctx = nil
		}
	}()
	return extractWithGotreesitter(targetPath, projectRoot)
}

// extractUsedSymbols walks the AST of a parsed file to find which symbols
// from each import are actually referenced via selector expressions (e.g. `pkg.Func`).
// Returns a map of local-package-name -> list of used symbol names.
func extractUsedSymbols(af *ast.File, imports []importInfo) map[string][]string {
	// Build a lookup: local name -> set of actual imported paths (for alias detection)
	importByName := make(map[string]string)
	for _, imp := range imports {
		importByName[imp.localName] = imp.path
	}

	// Collect all used symbols per local name
	used := make(map[string]map[string]bool) // localName -> symbolName -> true

	ast.Inspect(af, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		// X must be an identifier (package name)
		xIdent, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}

		pkgName := xIdent.Name
		symName := sel.Sel.Name

		// Check if this identifier is an imported package
		if _, isImport := importByName[pkgName]; isImport {
			if used[pkgName] == nil {
				used[pkgName] = make(map[string]bool)
			}
			// Filter: only track exported symbols (capitalized)
			// and common pseudo-symbols we want to skip
			if symName != "" && ast.IsExported(symName) {
				used[pkgName][symName] = true
			}
		}
		return true
	})

	// Convert to sorted string slices
	result := make(map[string][]string)
	for pkgName, symSet := range used {
		if len(symSet) > 0 {
			syms := make([]string, 0, len(symSet))
			for s := range symSet {
				syms = append(syms, s)
			}
			// Sort for deterministic output
			sort.Strings(syms)
			result[pkgName] = syms
		}
	}

	return result
}

func (t *ContextResolverTool) getOrBuildIndex(projectDir string) *cachedProject {
	t.cacheMu.RLock()
	proj, ok := t.pkgCache[projectDir]
	t.cacheMu.RUnlock()

	if ok && time.Since(proj.grabbedAt) < 5*time.Second {
		return proj
	}

	proj = buildProjectIndex(projectDir)

	t.cacheMu.Lock()
	t.pkgCache[projectDir] = proj
	t.cacheMu.Unlock()
	return proj
}

func buildProjectIndex(projectDir string) *cachedProject {
	proj := &cachedProject{
		projectDir: projectDir,
		pkgMap:     make(map[string]*pkgInfo),
		allGoFiles: make([]string, 0),
		testFiles:  make([]string, 0),
		grabbedAt:  time.Now(),
	}

	filepath.WalkDir(projectDir, func(path string, d os.DirEntry, err error) error {
		if err != nil { return nil }
		if d.IsDir() && shouldSkipDir(d.Name()) { return filepath.SkipDir }
		if d.IsDir() { return nil }
		if !strings.HasSuffix(path, ".go") { return nil }

		if strings.HasSuffix(path, "_test.go") {
			proj.testFiles = append(proj.testFiles, path)
			return nil
		}

		proj.allGoFiles = append(proj.allGoFiles, path)

		pkgDir := filepath.Dir(path)
		fi, ok := proj.pkgMap[pkgDir]
		if !ok {
			fi = &pkgInfo{dir: pkgDir, files: make(map[string]*fileInfo)}
			proj.pkgMap[pkgDir] = fi
		}

		fset := token.NewFileSet()
		af, parseErr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if parseErr != nil { return nil }

		if fi.name == "" && af.Name != nil {
			fi.name = af.Name.Name
		}

		fInfo := &fileInfo{path: path}
		for _, imp := range af.Imports {
			impPath := strings.Trim(imp.Path.Value, "\"")
			fInfo.imports = append(fInfo.imports, impPath)
		}
		fi.files[path] = fInfo
		return nil
	})

	return proj
}

func findDefFiles(proj *cachedProject, importPath string, max int) []string {
	var results []string

	// Strip module prefix to get relative directory; handle root package
	base := filepath.Base(proj.projectDir)
	var importRel string
	if strings.HasPrefix(importPath, base+"/") {
		importRel = importPath[len(base)+1:]
	} else if importPath == base {
		importRel = ""
	} else {
		importRel = importPath
	}

	for _, f := range proj.allGoFiles {
		rel, _ := filepath.Rel(proj.projectDir, f)
		// Check that the file path matches the import as a directory prefix
		// (e.g., import "internal/client" matches "internal/client/client.go")
		if strings.HasPrefix(rel, importRel) &&
			(len(rel) == len(importRel) || rel[len(importRel)] == '/') {
			results = append(results, f)
			if len(results) >= max { break }
		}
	}
	return results
}

func extractLocalSymbols(fset *token.FileSet, af *ast.File) []LocalSymbol {
	var symbols []LocalSymbol
	for _, decl := range af.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Name != nil {
				kind := "function"
				if d.Recv != nil { kind = "method" }
				symbols = append(symbols, LocalSymbol{
					Name: d.Name.Name, Kind: kind,
					Line: fset.Position(d.Pos()).Line,
				})
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					kind := "type"
					if s.Type != nil {
						switch s.Type.(type) {
						case *ast.StructType: kind = "struct"
						case *ast.InterfaceType: kind = "interface"
						}
					}
					symbols = append(symbols, LocalSymbol{
						Name: s.Name.Name, Kind: kind,
						Line: fset.Position(s.Pos()).Line,
					})
				case *ast.ValueSpec:
					kind := "var"
					if d.Tok == token.CONST { kind = "const" }
					for _, name := range s.Names {
						symbols = append(symbols, LocalSymbol{
							Name: name.Name, Kind: kind,
							Line: fset.Position(name.Pos()).Line,
						})
					}
				}
			}
		}
	}
	return symbols
}

func extractPackageName(content string) string {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "package ") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, "package "))
		}
	}
	return ""
}

func extractPackageNameFromReader(path string) string {
	data, err := os.ReadFile(path)
	if err != nil { return "" }
	return extractPackageName(string(data))
}

func readModuleName(projectDir string) string {
	data, err := os.ReadFile(filepath.Join(projectDir, "go.mod"))
	if err != nil { return "" }
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, "module "))
		}
	}
	return ""
}

func detectLanguage(ext string) string {
	switch ext {
	case ".go": return "go"
	case ".py": return "python"
	case ".js", ".jsx": return "javascript"
	case ".ts", ".tsx": return "typescript"
	case ".kt", ".kts": return "kotlin"
	case ".java": return "java"
	case ".rs": return "rust"
	case ".rb": return "ruby"
	case ".cs": return "csharp"
	case ".swift": return "swift"
	case ".c", ".h": return "c"
	case ".cpp", ".cc", ".hpp": return "cpp"
	default: return "unknown"
	}
}

func shouldSkipDir(name string) bool {
	switch name {
	case ".git", ".svn", ".hg", "node_modules", "vendor", ".cache",
		"__pycache__", ".venv", "venv", "env", ".tox", "dist", "build",
		".next", ".nuxt", "target", "bin", "obj":
		return true
	}
	return strings.HasPrefix(name, ".")
}

func isStdLibImport(importPath, moduleName string, proj *cachedProject) bool {
	// If it starts with the project module name, it's local, not stdlib
	// Must check trailing '/' or exact match to avoid prefix collisions
	// (e.g. module "late" must not match import "late-tool/foo")
	if moduleName != "" {
		if importPath == moduleName || strings.HasPrefix(importPath, moduleName+"/") {
			return false
		}
	}

	// Check if any local file directory matches this import
	for _, f := range proj.allGoFiles {
		rel, _ := filepath.Rel(proj.projectDir, f)
		dir := filepath.Dir(rel)
		if dir == importPath || strings.HasPrefix(dir, importPath+"/") {
			return false
		}
	}

	first := strings.Split(importPath, "/")[0]
	return !strings.Contains(first, ".")
}

func (t *ContextResolverTool) RequiresConfirmation(args json.RawMessage) bool { return false }

func (t *ContextResolverTool) CallString(args json.RawMessage) string {
	target := getToolParam(args, "target_file")
	if target == "" { return "Resolving context..." }
	return fmt.Sprintf("Resolving context for %s...", truncate(target, 50))
}

func formatResult(result ContextResult) (string, error) {
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal result: %w", err)
	}
	return string(data), nil
}

var _ Tool = (*ContextResolverTool)(nil)
