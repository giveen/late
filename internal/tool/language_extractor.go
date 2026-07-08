package tool

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

// -------- language-specific tree-sitter queries --------

// rubyImportQuery matches all method calls with a single string argument.
// Filtering to require/require_relative/load is done in Go code because
// tree-sitter predicates don't work reliably with this version of gotreesitter.
const rubyImportQuery = `
(call method: (identifier) @method arguments: (argument_list (string) @import))
`

// rubyDefQuery matches method/singleton_method/class/module definitions
const rubyDefQuery = `
[
  (method name: (identifier) @name) @definition.method
  (singleton_method name: (identifier) @name) @definition.method
  (class name: (constant) @name) @definition.class
  (module name: (constant) @name) @definition.module
]
`

// pythonImportQuery captures import and from-import statements
const pythonImportQuery = `
[
  (import_statement name: (dotted_name) @import)
  (import_from_statement module_name: (dotted_name) @from name: (dotted_name) @import)
]
`

// pythonDefQuery captures function and class definitions
const pythonDefQuery = `
[
  (function_definition name: (identifier) @name) @definition.function
  (class_definition name: (identifier) @name) @definition.class
]
`

// jsImportQuery captures ES module imports and require calls
const jsImportQuery = `
[
  (import_statement source: (string) @import)
  (call_expression function: (identifier) @method (#eq? @method "require") arguments: (arguments (string) @import))
]
`

// jsDefQuery captures function, class, and method definitions (arrow functions
// are anonymous in tree-sitter's JS grammar — they have no 'name' field)
const jsDefQuery = `
[
  (function_declaration name: (identifier) @name) @definition.function
  (class_declaration name: (identifier) @name) @definition.class
  (method_definition name: (property_identifier) @name) @definition.method
]
`

// languageQueries maps language names to their extraction queries
type langQueries struct {
	ImportQuery string
	DefQuery    string
}

var languageQueryMap = map[string]langQueries{
	"ruby":       {ImportQuery: rubyImportQuery, DefQuery: rubyDefQuery},
	"python":     {ImportQuery: pythonImportQuery, DefQuery: pythonDefQuery},
	"javascript": {ImportQuery: jsImportQuery, DefQuery: jsDefQuery},
	"typescript": {ImportQuery: jsImportQuery, DefQuery: jsDefQuery},
	"tsx":        {ImportQuery: jsImportQuery, DefQuery: jsDefQuery},
}

// -------- lazy-loaded grammar cache --------

type grammarCacheEntry struct {
	lang      *gotreesitter.Language
	langEntry *grammars.LangEntry
}

var (
	grammarCache   map[string]*grammarCacheEntry
	grammarCacheMu sync.RWMutex
)

func getOrLoadGrammar(filename string) *grammarCacheEntry {
	entry := grammars.DetectLanguage(filename)
	if entry == nil {
		return nil
	}

	grammarCacheMu.RLock()
	cached, ok := grammarCache[entry.Name]
	grammarCacheMu.RUnlock()
	if ok {
		return cached
	}

	grammarCacheMu.Lock()
	defer grammarCacheMu.Unlock()

	// Double-check after acquiring write lock
	if cached, ok := grammarCache[entry.Name]; ok {
		return cached
	}

	// Lazy-load the grammar — first call triggers decompression
	lang := entry.Language()
	if lang == nil {
		return nil
	}

	if grammarCache == nil {
		grammarCache = make(map[string]*grammarCacheEntry)
	}
	cached = &grammarCacheEntry{lang: lang, langEntry: entry}
	grammarCache[entry.Name] = cached
	return cached
}

// -------- results --------

// extractedContext holds data extracted via gotreesitter for a single file.
type extractedContext struct {
	Language     string
	Package      string
	Imports      []ResolvedImport
	LocalSymbols []LocalSymbol
}

// -------- main extraction function --------

// extractWithGotreesitter uses tree-sitter to extract imports and definitions
// from any supported language file. Returns nil if parsing fails or language
// is not supported.
func extractWithGotreesitter(targetPath, projectRoot string) *extractedContext {
	start := time.Now()
	defer func() {
		elapsed := time.Since(start)
		if elapsed > time.Millisecond {
			// Only log slow parses (>1ms) for debugging
		}
	}()

	gc := getOrLoadGrammar(filepath.Base(targetPath))
	if gc == nil {
		return nil
	}

	src, err := os.ReadFile(targetPath)
	if err != nil {
		return nil
	}

	parser := gotreesitter.NewParser(gc.lang)
	tree, err := parser.Parse(src)
	if err != nil || tree == nil {
		return nil
	}

	ctx := &extractedContext{
		Language: gc.langEntry.Name,
	}

	// 1. Try gotreesitter's built-in ExtractImports (Go, Java, Python, Starlark)
	builtinImports := gotreesitter.ExtractImports(tree)
	for _, imp := range builtinImports {
		localName := imp.Name
		if imp.Alias != "" {
			localName = imp.Alias
		}
		ctx.Imports = append(ctx.Imports, ResolvedImport{
			Path:      imp.Path,
			LocalName: localName,
		})
	}

	// 2. Try gotreesitter's built-in ExtractDefinitionSpans (Go, JS, TS, Python, Java)
	builtinDefs := gotreesitter.ExtractDefinitionSpans(tree)
	for _, def := range builtinDefs {
		ctx.LocalSymbols = append(ctx.LocalSymbols, LocalSymbol{
			Name: def.Name,
			Kind: def.Kind,
		})
	}

	// 3. If built-in ExtractImports returned nothing, try language-specific queries
	if len(builtinImports) == 0 || len(builtinDefs) == 0 {
		queries, hasQueries := languageQueryMap[gc.langEntry.Name]
		if hasQueries {
			if len(builtinImports) == 0 && queries.ImportQuery != "" {
				if importRefs := executeQuery(tree, gc.lang, src, queries.ImportQuery, "import"); len(importRefs) > 0 {
					// Filter to only require/load calls for Ruby (structural query matches all method+string calls)
					for _, ref := range importRefs {
						// LocalName holds the method name from @method capture
						methodName := ref.LocalName
						if gc.langEntry.Name == "ruby" && !isRubyImportMethod(methodName) {
							continue
						}
						// Strip quotes from import paths
						path := strings.Trim(ref.Path, "'\"")
						ctx.Imports = append(ctx.Imports, ResolvedImport{
							Path:      path,
							LocalName: "",
						})
					}
				}
			}
			if len(builtinDefs) == 0 && queries.DefQuery != "" {
				if symRefs := executeQuery(tree, gc.lang, src, queries.DefQuery, "definition"); len(symRefs) > 0 {
					for _, sym := range symRefs {
						ctx.LocalSymbols = append(ctx.LocalSymbols, LocalSymbol{
							Name: sym.Path,
							Kind: sym.LocalName,
						})
					}
				}
			}
		}
	}

	return ctx
}

// executeQuery runs a tree-sitter query and extracts named captures.
// For import queries, it returns ResolvedImport entries.
// For definition queries, it maps captures to a ResolvedImport-like format
// (reusing the struct for simplicity — Path holds name, LocalName holds kind).
func executeQuery(tree *gotreesitter.Tree, lang *gotreesitter.Language, src []byte, queryStr string, qtype string) []ResolvedImport {
	q, err := gotreesitter.NewQuery(queryStr, lang)
	if err != nil {
		return nil
	}

	root := tree.RootNode()
	var results []ResolvedImport
	seen := make(map[string]bool)
	c := q.Exec(root, lang, src)

	for {
		m, ok := c.NextMatch()
		if !ok {
			break
		}

		var name, kind string
		for _, cap := range m.Captures {
			text := string(cap.Node.Text(src))
			switch cap.Name {
			case "import", "from":
				name = text
			case "name":
				name = text
			case "method":
				kind = text
			case "definition":
				// kind is embedded in the capture name like "definition.function"
			}
		}

		if name == "" || seen[name] {
			continue
		}
		seen[name] = true

		// Use capture names that contain dots as kind markers
		for _, cap := range m.Captures {
			if len(cap.Name) > 11 && cap.Name[:11] == "definition." {
				kind = cap.Name[11:]
				break
			}
		}

		results = append(results, ResolvedImport{
			Path:      name,
			LocalName: kind,
		})
	}

	return results
}

// detectProjectRoot walks up from targetPath looking for language-specific
// project root markers. Returns the first found root directory.
func detectProjectRoot(targetPath string) string {
	dir := targetPath
	if !isDir(dir) {
		dir = filepath.Dir(dir)
	}

	// Walk up looking for known project root markers
	for dir != "/" {
		markers := []string{
			"go.mod", "go.sum",          // Go
			"Gemfile", "Gemfile.lock",   // Ruby
			"Cargo.toml", "Cargo.lock",  // Rust
			"package.json",              // JS/TS
			"pyproject.toml", "setup.py", "setup.cfg", // Python
			"pom.xml", "build.gradle", "build.gradle.kts", // Java/Kotlin
			"*.csproj", "*.sln",          // C#
			"CMakeLists.txt",             // C/C++
			"Package.swift",              // Swift
		}
		for _, marker := range markers {
			matches, err := filepath.Glob(filepath.Join(dir, marker))
			if err == nil && len(matches) > 0 {
				return dir
			}
		}
		dir = filepath.Dir(dir)
	}
	return ""
}

func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// findTestFiles discovers test files in a project for the given language.
func findTestFiles(projectRoot, language string) []TestFile {
	var results []TestFile
	patterns := testFilePatterns(language)
	if len(patterns) == 0 {
		return nil
	}

	for _, pattern := range patterns {
		matches, err := filepath.Glob(filepath.Join(projectRoot, pattern))
		if err != nil {
			continue
		}
		for _, m := range matches {
			rel, _ := filepath.Rel(projectRoot, m)
			results = append(results, TestFile{
				Path:       rel,
				Confidence: "direct",
			})
		}
	}
	return results
}

// testFilePatterns returns glob patterns for test files per language.
func testFilePatterns(language string) []string {
	switch language {
	case "go":
		return []string{"**/*_test.go"}
	case "ruby":
		return []string{"**/*_spec.rb", "**/spec/**/*.rb", "**/test/**/*_test.rb"}
	case "python":
		return []string{"**/test_*.py", "**/*_test.py", "**/tests/**/*.py"}
	case "javascript", "typescript", "tsx":
		return []string{"**/*.test.js", "**/*.test.ts", "**/*.spec.js", "**/*.spec.ts", "**/__tests__/**/*.js", "**/__tests__/**/*.ts"}
	case "rust":
		return []string{"**/*_test.rs", "**/tests/**/*.rs"}
	case "java", "kotlin":
		return []string{"**/*Test.java", "**/*Test.kt", "**/*Spec.kt"}
	case "csharp":
		return []string{"**/*Test.cs", "**/*Tests.cs"}
	case "cpp", "c":
		return []string{"**/*_test.cpp", "**/*_test.cc", "**/*_test.c"}
	}
	return nil
}

// findSiblingFiles discovers other source files in the same directory as targetPath.
func findSiblingFiles(projectRoot, targetPath string) []string {
	targetDir := filepath.Dir(targetPath)
	entries, err := os.ReadDir(targetDir)
	if err != nil {
		return nil
	}

	var siblings []string
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		full := filepath.Join(targetDir, e.Name())
		if e.Name() == filepath.Base(targetPath) {
			continue
		}
		rel, _ := filepath.Rel(projectRoot, full)
		if isSourceFile(e.Name()) {
			siblings = append(siblings, rel)
		}
	}
	return siblings
}

// isSourceFile returns true if the filename looks like a source file we should
// include in sibling results.
func isSourceFile(name string) bool {
	ext := filepath.Ext(name)
	switch ext {
	case ".go", ".rb", ".py", ".js", ".ts", ".tsx", ".jsx",
		".rs", ".java", ".kt", ".kts", ".cs", ".cpp", ".cc",
		".c", ".h", ".hpp", ".swift", ".scala", ".dart":
		return true
	}
	return false
}

// isRubyImportMethod returns true for require/require_relative/load method names.
func isRubyImportMethod(method string) bool {
	return method == "require" || method == "require_relative" || method == "load"
}
