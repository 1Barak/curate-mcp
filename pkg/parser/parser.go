package parser

import (
	"bufio"
	"bytes"
	"errors"
	goast "go/ast"
	goparser "go/parser"
	gotoken "go/token"
	"path/filepath"
	"regexp"
	"strings"
)

// Standard errors for parser operations.
var (
	ErrUnsupportedLanguage = errors.New("parser: unsupported language extension")
	ErrNotImplemented       = errors.New("parser: tree-sitter grammar engine not implemented")
)

// SymbolKind represents the type of an extracted code definition.
type SymbolKind string

const (
	KindFunction  SymbolKind = "function"
	KindMethod    SymbolKind = "method"
	KindClass     SymbolKind = "class"
	KindInterface SymbolKind = "interface"
	KindStruct    SymbolKind = "struct"
)

// Symbol holds metadata for an extracted definition.
type Symbol struct {
	Name      string     `json:"name"`
	Kind      SymbolKind `json:"kind"`
	StartLine int        `json:"start_line"`
}

// ParseResult contains the extracted AST symbols, imports, and calls for a file.
type ParseResult struct {
	FilePath string   `json:"file_path"`
	Language string   `json:"language"`
	Imports  []string `json:"imports"`
	Symbols  []Symbol `json:"symbols"`
	Calls    []string `json:"calls"`
}

// Parser defines the unified interface for AST parsing across languages.
type Parser interface {
	Parse(filePath string, content []byte) (*ParseResult, error)
	DetectLanguage(filePath string) (string, error)
	SupportedLanguages() []string
}

// Dispatcher coordinates multi-language AST parsing across the 7 core languages.
type Dispatcher struct{}

// NewDispatcher creates a new Tree-Sitter multi-language dispatcher.
func NewDispatcher() *Dispatcher {
	return &Dispatcher{}
}

// SupportedLanguages returns the list of normalized language identifiers supported.
func (d *Dispatcher) SupportedLanguages() []string {
	return []string{
		"go",
		"python",
		"typescript",
		"javascript",
		"rust",
		"c",
		"cpp",
		"java",
	}
}

// DetectLanguage maps file extensions to canonical language identifiers.
func (d *Dispatcher) DetectLanguage(filePath string) (string, error) {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".go":
		return "go", nil
	case ".py":
		return "python", nil
	case ".ts", ".tsx":
		return "typescript", nil
	case ".js", ".jsx", ".mjs", ".cjs":
		return "javascript", nil
	case ".rs":
		return "rust", nil
	case ".c", ".h":
		return "c", nil
	case ".cpp", ".hpp", ".cc":
		return "cpp", nil
	case ".java":
		return "java", nil
	default:
		return "", ErrUnsupportedLanguage
	}
}

// Parse extracts symbols, imports, and calls from the file content across the 7 core languages.
func (d *Dispatcher) Parse(filePath string, content []byte) (*ParseResult, error) {
	lang, err := d.DetectLanguage(filePath)
	if err != nil {
		return nil, err
	}

	res := &ParseResult{
		FilePath: filePath,
		Language: lang,
		Imports:  []string{},
		Symbols:  []Symbol{},
		Calls:    []string{},
	}

	switch lang {
	case "go":
		return parseGo(filePath, content, res)
	case "python":
		return parsePython(content, res)
	case "typescript", "javascript":
		return parseJSorTS(content, res)
	case "rust":
		return parseRust(content, res)
	case "c", "cpp":
		return parseCAndCpp(content, res)
	case "java":
		return parseJava(content, res)
	default:
		return nil, ErrUnsupportedLanguage
	}
}

// --- Go parser using native go/parser standard library ---
func parseGo(filePath string, content []byte, res *ParseResult) (*ParseResult, error) {
	fset := gotoken.NewFileSet()
	f, err := goparser.ParseFile(fset, filePath, content, goparser.AllErrors)
	if err != nil {
		return nil, err
	}

	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		res.Imports = append(res.Imports, path)
	}

	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *goast.GenDecl:
			for _, spec := range d.Specs {
				if ts, ok := spec.(*goast.TypeSpec); ok {
					line := fset.Position(ts.Pos()).Line
					switch ts.Type.(type) {
					case *goast.InterfaceType:
						res.Symbols = append(res.Symbols, Symbol{
							Name:      ts.Name.Name,
							Kind:      KindInterface,
							StartLine: line,
						})
					case *goast.StructType:
						res.Symbols = append(res.Symbols, Symbol{
							Name:      ts.Name.Name,
							Kind:      KindStruct,
							StartLine: line,
						})
					}
				}
			}
		case *goast.FuncDecl:
			line := fset.Position(d.Pos()).Line
			kind := KindFunction
			if d.Recv != nil && len(d.Recv.List) > 0 {
				kind = KindMethod
			}
			res.Symbols = append(res.Symbols, Symbol{
				Name:      d.Name.Name,
				Kind:      kind,
				StartLine: line,
			})
		}
	}
	return res, nil
}

// --- Python parser ---
var (
	pyImportRe  = regexp.MustCompile(`^\s*import\s+([a-zA-Z0-9_.]+)`)
	pyFromRe    = regexp.MustCompile(`^\s*from\s+([a-zA-Z0-9_.]+)\s+import`)
	pyClassRe   = regexp.MustCompile(`^\s*class\s+([a-zA-Z0-9_]+)`)
	pyDefRe     = regexp.MustCompile(`^(\s*)def\s+([a-zA-Z0-9_]+)`)
)

func parsePython(content []byte, res *ParseResult) (*ParseResult, error) {
	scanner := bufio.NewScanner(bytes.NewReader(content))
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		if m := pyImportRe.FindStringSubmatch(line); len(m) > 1 {
			res.Imports = append(res.Imports, m[1])
			continue
		}
		if m := pyFromRe.FindStringSubmatch(line); len(m) > 1 {
			res.Imports = append(res.Imports, m[1])
			continue
		}
		if m := pyClassRe.FindStringSubmatch(line); len(m) > 1 {
			res.Symbols = append(res.Symbols, Symbol{
				Name:      m[1],
				Kind:      KindClass,
				StartLine: lineNum,
			})
			continue
		}
		if m := pyDefRe.FindStringSubmatch(line); len(m) > 2 {
			indent := m[1]
			name := m[2]
			kind := KindFunction
			if len(indent) > 0 {
				kind = KindMethod
			}
			res.Symbols = append(res.Symbols, Symbol{
				Name:      name,
				Kind:      kind,
				StartLine: lineNum,
			})
		}
	}
	return res, scanner.Err()
}

// --- TypeScript / JavaScript parser ---
var (
	jsImportFromRe = regexp.MustCompile(`import\s+.*?from\s+['"]([^'"]+)['"]`)
	jsImportBareRe = regexp.MustCompile(`import\s+['"]([^'"]+)['"]`)
	jsRequireRe    = regexp.MustCompile(`require\s*\(\s*['"]([^'"]+)['"]\s*\)`)
	jsInterfaceRe  = regexp.MustCompile(`(?:export\s+)?interface\s+([a-zA-Z0-9_]+)`)
	jsClassRe      = regexp.MustCompile(`(?:export\s+)?class\s+([a-zA-Z0-9_]+)`)
	jsFuncRe       = regexp.MustCompile(`(?:export\s+)?(?:async\s+)?function\s+([a-zA-Z0-9_]+)`)
	jsMethodRe     = regexp.MustCompile(`^\s*(?:(?:public|private|protected|async|static)\s+)*([a-zA-Z0-9_]+)\s*\([^)]*\)\s*(?::\s*[^{]+)?\s*\{`)
)

func parseJSorTS(content []byte, res *ParseResult) (*ParseResult, error) {
	scanner := bufio.NewScanner(bytes.NewReader(content))
	lineNum := 0
	braceDepth := 0
	classBraceDepth := -1

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		// Imports
		if m := jsImportFromRe.FindStringSubmatch(line); len(m) > 1 {
			res.Imports = append(res.Imports, m[1])
		} else if m := jsImportBareRe.FindStringSubmatch(line); len(m) > 1 {
			res.Imports = append(res.Imports, m[1])
		} else if m := jsRequireRe.FindStringSubmatch(line); len(m) > 1 {
			res.Imports = append(res.Imports, m[1])
		}

		// Interfaces
		if m := jsInterfaceRe.FindStringSubmatch(line); len(m) > 1 {
			res.Symbols = append(res.Symbols, Symbol{
				Name:      m[1],
				Kind:      KindInterface,
				StartLine: lineNum,
			})
		}

		// Classes
		if m := jsClassRe.FindStringSubmatch(line); len(m) > 1 {
			res.Symbols = append(res.Symbols, Symbol{
				Name:      m[1],
				Kind:      KindClass,
				StartLine: lineNum,
			})
			classBraceDepth = braceDepth
		} else if classBraceDepth >= 0 && braceDepth > classBraceDepth {
			// Check methods inside class
			if m := jsMethodRe.FindStringSubmatch(line); len(m) > 1 {
				name := m[1]
				if name != "constructor" && name != "if" && name != "for" && name != "switch" && name != "while" {
					res.Symbols = append(res.Symbols, Symbol{
						Name:      name,
						Kind:      KindMethod,
						StartLine: lineNum,
					})
				}
			}
		} else {
			// Top-level / exported functions
			if m := jsFuncRe.FindStringSubmatch(line); len(m) > 1 {
				res.Symbols = append(res.Symbols, Symbol{
					Name:      m[1],
					Kind:      KindFunction,
					StartLine: lineNum,
				})
			}
		}

		// Update brace depth
		for _, ch := range line {
			if ch == '{' {
				braceDepth++
			} else if ch == '}' {
				braceDepth--
				if classBraceDepth >= 0 && braceDepth <= classBraceDepth {
					classBraceDepth = -1
				}
			}
		}
	}
	return res, scanner.Err()
}

// --- Rust parser ---
var (
	rsUseRe       = regexp.MustCompile(`^\s*use\s+([^;]+);`)
	rsTraitRe     = regexp.MustCompile(`(?:pub\s+)?trait\s+([a-zA-Z0-9_]+)`)
	rsStructRe    = regexp.MustCompile(`(?:pub\s+)?struct\s+([a-zA-Z0-9_]+)`)
	rsImplRe      = regexp.MustCompile(`^\s*impl(?:\s+.*?)?\s+([a-zA-Z0-9_]+)`)
	rsFnRe        = regexp.MustCompile(`(?:pub\s+)?(?:async\s+)?fn\s+([a-zA-Z0-9_]+)`)
)

func parseRust(content []byte, res *ParseResult) (*ParseResult, error) {
	scanner := bufio.NewScanner(bytes.NewReader(content))
	lineNum := 0
	braceDepth := 0
	implBraceDepth := -1

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		// Imports
		if m := rsUseRe.FindStringSubmatch(line); len(m) > 1 {
			res.Imports = append(res.Imports, strings.TrimSpace(m[1]))
			continue
		}

		// Traits (interfaces)
		if m := rsTraitRe.FindStringSubmatch(line); len(m) > 1 {
			res.Symbols = append(res.Symbols, Symbol{
				Name:      m[1],
				Kind:      KindInterface,
				StartLine: lineNum,
			})
		}

		// Structs
		if m := rsStructRe.FindStringSubmatch(line); len(m) > 1 {
			res.Symbols = append(res.Symbols, Symbol{
				Name:      m[1],
				Kind:      KindStruct,
				StartLine: lineNum,
			})
		}

		// Impl blocks
		if rsImplRe.MatchString(line) {
			implBraceDepth = braceDepth
		}

		// Functions / Methods
		if m := rsFnRe.FindStringSubmatch(line); len(m) > 1 {
			kind := KindFunction
			if implBraceDepth >= 0 && braceDepth > implBraceDepth {
				kind = KindMethod
			}
			res.Symbols = append(res.Symbols, Symbol{
				Name:      m[1],
				Kind:      kind,
				StartLine: lineNum,
			})
		}

		for _, ch := range line {
			if ch == '{' {
				braceDepth++
			} else if ch == '}' {
				braceDepth--
				if implBraceDepth >= 0 && braceDepth <= implBraceDepth {
					implBraceDepth = -1
				}
			}
		}
	}
	return res, scanner.Err()
}

// --- C / C++ parser ---
var (
	cIncludeRe = regexp.MustCompile(`#include\s*[<"]([^>"]+)[>"]`)
	cClassRe   = regexp.MustCompile(`(?:class|struct)\s+([a-zA-Z0-9_]+)\s*(?::|\{)`)
	cMethodRe  = regexp.MustCompile(`^\s*(?:(?:void\*?|[a-zA-Z0-9_:]+(?:\s*\*+|\s*&)?)\s+)+([a-zA-Z0-9_]+)\s*\([^)]*\)\s*\{`)
)

func parseCAndCpp(content []byte, res *ParseResult) (*ParseResult, error) {
	scanner := bufio.NewScanner(bytes.NewReader(content))
	lineNum := 0
	braceDepth := 0
	classBraceDepth := -1

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		// Includes
		if m := cIncludeRe.FindStringSubmatch(line); len(m) > 1 {
			res.Imports = append(res.Imports, m[1])
			continue
		}

		// Class
		if m := cClassRe.FindStringSubmatch(line); len(m) > 1 {
			res.Symbols = append(res.Symbols, Symbol{
				Name:      m[1],
				Kind:      KindClass,
				StartLine: lineNum,
			})
			classBraceDepth = braceDepth
		} else if m := cMethodRe.FindStringSubmatch(line); len(m) > 1 {
			name := m[1]
			if name != "if" && name != "while" && name != "for" && name != "switch" {
				kind := KindFunction
				if classBraceDepth >= 0 && braceDepth > classBraceDepth {
					kind = KindMethod
				}
				res.Symbols = append(res.Symbols, Symbol{
					Name:      name,
					Kind:      kind,
					StartLine: lineNum,
				})
			}
		}

		for _, ch := range line {
			if ch == '{' {
				braceDepth++
			} else if ch == '}' {
				braceDepth--
				if classBraceDepth >= 0 && braceDepth <= classBraceDepth {
					classBraceDepth = -1
				}
			}
		}
	}
	return res, scanner.Err()
}

// --- Java parser ---
var (
	javaImportRe    = regexp.MustCompile(`^\s*import\s+([^;]+);`)
	javaInterfaceRe = regexp.MustCompile(`(?:public\s+|protected\s+|private\s+)?interface\s+([a-zA-Z0-9_]+)`)
	javaClassRe     = regexp.MustCompile(`(?:public\s+|protected\s+|private\s+)?class\s+([a-zA-Z0-9_]+)`)
	javaMethodRe    = regexp.MustCompile(`(?:public|protected|private|static|\s)+[\w<>\[\]]+\s+([a-zA-Z0-9_]+)\s*\([^)]*\)\s*\{`)
)

func parseJava(content []byte, res *ParseResult) (*ParseResult, error) {
	scanner := bufio.NewScanner(bytes.NewReader(content))
	lineNum := 0
	braceDepth := 0
	classBraceDepth := -1

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		if m := javaImportRe.FindStringSubmatch(line); len(m) > 1 {
			res.Imports = append(res.Imports, strings.TrimSpace(m[1]))
			continue
		}

		if m := javaInterfaceRe.FindStringSubmatch(line); len(m) > 1 {
			res.Symbols = append(res.Symbols, Symbol{
				Name:      m[1],
				Kind:      KindInterface,
				StartLine: lineNum,
			})
		} else if m := javaClassRe.FindStringSubmatch(line); len(m) > 1 {
			res.Symbols = append(res.Symbols, Symbol{
				Name:      m[1],
				Kind:      KindClass,
				StartLine: lineNum,
			})
			classBraceDepth = braceDepth
		} else if classBraceDepth >= 0 && braceDepth > classBraceDepth {
			if m := javaMethodRe.FindStringSubmatch(line); len(m) > 1 {
				name := m[1]
				if name != "if" && name != "for" && name != "while" && name != "switch" {
					res.Symbols = append(res.Symbols, Symbol{
						Name:      name,
						Kind:      KindMethod,
						StartLine: lineNum,
					})
				}
			}
		}

		for _, ch := range line {
			if ch == '{' {
				braceDepth++
			} else if ch == '}' {
				braceDepth--
				if classBraceDepth >= 0 && braceDepth <= classBraceDepth {
					classBraceDepth = -1
				}
			}
		}
	}
	return res, scanner.Err()
}
