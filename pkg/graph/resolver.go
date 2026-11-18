package graph

import (
	"path/filepath"
	"strings"
	"unicode"
)

// matchesImport determines if importPath referenced by sourceFilePath corresponds
// to the target file targetFilePath.
func matchesImport(sourceFilePath, importPath, targetFilePath string) bool {
	if sourceFilePath == targetFilePath {
		return false
	}

	srcDir := filepath.ToSlash(filepath.Dir(sourceFilePath))
	tgtPath := filepath.ToSlash(targetFilePath)
	tgtDir := filepath.ToSlash(filepath.Dir(tgtPath))
	tgtExt := filepath.Ext(tgtPath)
	tgtNoExt := strings.TrimSuffix(tgtPath, tgtExt)
	tgtBase := filepath.Base(tgtNoExt)

	cleanImp := filepath.ToSlash(strings.TrimSpace(importPath))
	if cleanImp == "" {
		return false
	}

	// 1. Exact match against target path or target path without extension
	if cleanImp == tgtPath || cleanImp == tgtNoExt {
		return true
	}

	// 2. Relative import starting with . or .. (e.g. "./auth", "../crypto")
	if strings.HasPrefix(cleanImp, ".") {
		resolved := filepath.ToSlash(filepath.Clean(filepath.Join(srcDir, cleanImp)))
		if resolved == tgtPath || resolved == tgtNoExt {
			return true
		}
		// Match index / init files: e.g. "./utils" matching "src/utils/index.ts"
		if resolved == tgtDir && (tgtBase == "index" || tgtBase == "__init__" || tgtBase == "mod") {
			return true
		}
	}

	// 3. Directory / Package matching (e.g. Go import "pkg/auth" or "curate-mcp/pkg/auth")
	if cleanImp == tgtDir {
		return true
	}
	if tgtDir != "." && (strings.HasSuffix(cleanImp, "/"+tgtDir) || strings.HasSuffix(tgtDir, "/"+cleanImp)) {
		return true
	}

	// 4. Dot-separated Python module (e.g. "service.auth" or "auth.verify")
	if strings.Contains(cleanImp, ".") && !strings.HasPrefix(cleanImp, ".") {
		dotAsSlash := strings.ReplaceAll(cleanImp, ".", "/")
		if dotAsSlash == tgtPath || dotAsSlash == tgtNoExt || dotAsSlash == tgtDir {
			return true
		}
		if tgtDir != "." && (strings.HasSuffix(dotAsSlash, "/"+tgtDir) || strings.HasSuffix(tgtDir, "/"+dotAsSlash)) {
			return true
		}
		if strings.HasSuffix(tgtNoExt, "/"+dotAsSlash) || strings.HasSuffix(dotAsSlash, "/"+tgtNoExt) {
			return true
		}
	}

	// 5. Rust crate:: or super::
	if strings.HasPrefix(cleanImp, "crate::") {
		rustMod := strings.ReplaceAll(strings.TrimPrefix(cleanImp, "crate::"), "::", "/")
		if rustMod == tgtNoExt || strings.HasSuffix(tgtNoExt, "/"+rustMod) || rustMod == tgtDir || strings.HasSuffix(tgtDir, "/"+rustMod) {
			return true
		}
	}

	// 6. Basename match within same directory
	if cleanImp == tgtBase && (tgtDir == srcDir || tgtDir == ".") {
		return true
	}

	return false
}

// matchesCall checks if a candidate symbol label matches the invoked call name
// (supporting simple names and qualified names like auth.Login or pkg::Func).
func matchesCall(candidateLabel, call string) bool {
	if candidateLabel == "" || call == "" {
		return false
	}
	if candidateLabel == call {
		return true
	}
	// Qualified call e.g. "auth.Login" -> candidate "Login"
	if idx := strings.LastIndex(call, "."); idx >= 0 && idx+1 < len(call) {
		if candidateLabel == call[idx+1:] {
			return true
		}
	}
	// Qualified call e.g. "auth::Login" -> candidate "Login"
	if idx := strings.LastIndex(call, "::"); idx >= 0 && idx+2 < len(call) {
		if candidateLabel == call[idx+2:] {
			return true
		}
	}
	return false
}

// isExported determines if a symbol definition is publicly exported according
// to language naming conventions and visibility rules.
func isExported(sym Node) bool {
	if len(sym.Label) == 0 {
		return false
	}
	ext := filepath.Ext(sym.FilePath)
	switch ext {
	case ".go":
		return unicode.IsUpper(rune(sym.Label[0]))
	case ".py":
		return !strings.HasPrefix(sym.Label, "_")
	default:
		// For TS/JS/Rust: private symbols start with '_' or '#'
		return !strings.HasPrefix(sym.Label, "_") && !strings.HasPrefix(sym.Label, "#")
	}
}

// ResolveCrossModuleEdges performs Phase 2 in-memory edge resolution for EdgeImports and EdgeCalls (RF-3).
// It maps imports between files and links calls between caller and callee symbols:
//   - EdgeImports: File -> File matching import paths
//   - EdgeCalls: Symbol -> Symbol matching function calls (both intra-file private and cross-module exported)
//
// Thread-safety: acquires e.mu.Lock() exclusively and performs atomic edge map replacement.
func (e *Engine) ResolveCrossModuleEdges() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// 1. Partition nodes into files and symbols
	var fileNodes []Node
	var symNodes []Node
	filesByID := make(map[string]Node)
	fileSymbols := make(map[string][]Node) // file path/id -> slice of symbol Nodes

	for _, n := range e.nodes {
		if n.Type == NodeFile {
			fileNodes = append(fileNodes, n)
			filesByID[n.ID] = n
			filesByID[n.FilePath] = n
		} else if n.Type == NodeSymbol {
			symNodes = append(symNodes, n)
			fileSymbols[n.FilePath] = append(fileSymbols[n.FilePath], n)
			// Also register under fileID if different from FilePath
			if colonIdx := strings.Index(n.ID, ":"); colonIdx > 0 {
				fileID := n.ID[:colonIdx]
				if fileID != n.FilePath {
					fileSymbols[fileID] = append(fileSymbols[fileID], n)
				}
			}
		}
	}

	// 2. Resolve EdgeImports (File -> File)
	var importEdges []Edge
	importedFiles := make(map[string]map[string]bool) // from file -> set of imported target files

	for _, f := range fileNodes {
		for _, imp := range f.Imports {
			for _, t := range fileNodes {
				if f.ID == t.ID {
					continue
				}
				if matchesImport(f.FilePath, imp, t.FilePath) {
					edge := Edge{
						From: f.ID,
						To:   t.ID,
						Type: EdgeImports,
					}
					importEdges = append(importEdges, edge)

					if importedFiles[f.ID] == nil {
						importedFiles[f.ID] = make(map[string]bool)
					}
					importedFiles[f.ID][t.ID] = true
					importedFiles[f.ID][t.FilePath] = true

					if importedFiles[f.FilePath] == nil {
						importedFiles[f.FilePath] = make(map[string]bool)
					}
					importedFiles[f.FilePath][t.ID] = true
					importedFiles[f.FilePath][t.FilePath] = true
				}
			}
		}
	}

	// 3. Resolve EdgeCalls (Symbol -> Symbol)
	var callEdges []Edge

	for _, caller := range symNodes {
		callerFile := caller.FilePath
		callerDir := filepath.ToSlash(filepath.Dir(callerFile))
		callerExt := filepath.Ext(callerFile)

		for _, call := range caller.Calls {
			resolved := false

			// Tier 1: Intra-file search (same file, exported or private)
			for _, candidate := range fileSymbols[callerFile] {
				if candidate.ID == caller.ID {
					continue
				}
				if matchesCall(candidate.Label, call) {
					callEdges = append(callEdges, Edge{
						From: caller.ID,
						To:   candidate.ID,
						Type: EdgeCalls,
					})
					resolved = true
					break
				}
			}
			if resolved {
				continue
			}

			// Tier 2: Same directory / package search (for Go and C/C++)
			if callerExt == ".go" || callerExt == ".c" || callerExt == ".cpp" {
				for _, otherFile := range fileNodes {
					if otherFile.FilePath == callerFile {
						continue
					}
					otherDir := filepath.ToSlash(filepath.Dir(otherFile.FilePath))
					if otherDir == callerDir {
						for _, candidate := range fileSymbols[otherFile.FilePath] {
							if matchesCall(candidate.Label, call) {
								callEdges = append(callEdges, Edge{
									From: caller.ID,
									To:   candidate.ID,
									Type: EdgeCalls,
								})
								resolved = true
								break
							}
						}
						if resolved {
							break
						}
					}
				}
			}
			if resolved {
				continue
			}

			// Tier 3: Imported files search (only exported symbols)
			targetSet := make(map[string]bool)
			for t := range importedFiles[callerFile] {
				targetSet[t] = true
			}
			if fNode, ok := filesByID[callerFile]; ok {
				for t := range importedFiles[fNode.ID] {
					targetSet[t] = true
				}
			}

			for targetKey := range targetSet {
				candidates := fileSymbols[targetKey]
				if len(candidates) == 0 {
					if tNode, ok := filesByID[targetKey]; ok {
						candidates = fileSymbols[tNode.FilePath]
					}
				}

				for _, candidate := range candidates {
					if isExported(candidate) && matchesCall(candidate.Label, call) {
						callEdges = append(callEdges, Edge{
							From: caller.ID,
							To:   candidate.ID,
							Type: EdgeCalls,
						})
						resolved = true
						break
					}
				}
				if resolved {
					break
				}
			}
		}
	}

	// 4. Rebuild edges atomically, preserving non-functional edges (EdgeDefines)
	newEdges := make(map[string]Edge)
	newAdj := make(map[string][]string)
	newPred := make(map[string][]string)

	// Keep existing defines edges
	for k, edge := range e.edges {
		if edge.Type != EdgeCalls && edge.Type != EdgeImports {
			newEdges[k] = edge
			newAdj[edge.From] = append(newAdj[edge.From], edge.To)
			newPred[edge.To] = append(newPred[edge.To], edge.From)
		}
	}

	// Add resolved import edges
	for _, edge := range importEdges {
		edgeKey := edge.From + "->" + edge.To + ":" + string(edge.Type)
		if _, exists := newEdges[edgeKey]; !exists {
			newEdges[edgeKey] = edge
			newAdj[edge.From] = append(newAdj[edge.From], edge.To)
			newPred[edge.To] = append(newPred[edge.To], edge.From)
		}
	}

	// Add resolved call edges
	for _, edge := range callEdges {
		edgeKey := edge.From + "->" + edge.To + ":" + string(edge.Type)
		if _, exists := newEdges[edgeKey]; !exists {
			newEdges[edgeKey] = edge
			newAdj[edge.From] = append(newAdj[edge.From], edge.To)
			newPred[edge.To] = append(newPred[edge.To], edge.From)
		}
	}

	e.edges = newEdges
	e.adjacency = newAdj
	e.predecessors = newPred

	return nil
}
