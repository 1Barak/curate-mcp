package graph

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// VirtualRootManifestID represents the canonical virtual root node ID for manifest entrypoints.
const VirtualRootManifestID = "$root:manifest"

// packageJSONSchema parses standard package.json entrypoints.
type packageJSONSchema struct {
	Main    string            `json:"main"`
	Bin     interface{}       `json:"bin"`
	Scripts map[string]string `json:"scripts"`
}

// mcpRegistrySchema parses mcp_registry.json command and args.
type mcpRegistrySchema struct {
	MCPs map[string]struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
	} `json:"mcps"`
}

var ignoredCommandBinaries = map[string]bool{
	"node": true, "node.exe": true,
	"python": true, "python.exe": true, "python3": true,
	"sh": true, "bash": true, "cmd": true, "powershell": true,
	"npx": true, "npm": true, "pnpm": true, "yarn": true,
	"cargo": true, "go": true,
}

// extractCommandTokens tokenizes a shell or process command string into words.
func extractCommandTokens(raw string) []string {
	fields := strings.Fields(raw)
	var tokens []string
	for _, f := range fields {
		clean := strings.Trim(f, `"'`)
		if clean != "" {
			tokens = append(tokens, clean)
		}
	}
	return tokens
}

// sanitizeAndValidatePath ensures candidate path is safely within root and not escaping.
func sanitizeAndValidatePath(root, candidate string) (string, bool) {
	if candidate == "" {
		return "", false
	}

	cleanCandidate := filepath.ToSlash(filepath.Clean(candidate))
	cleanCandidate = strings.TrimPrefix(cleanCandidate, "./")

	// Filter out command binaries and flags
	baseLower := strings.ToLower(filepath.Base(cleanCandidate))
	if ignoredCommandBinaries[baseLower] || strings.HasPrefix(candidate, "-") {
		return "", false
	}

	cleanRoot := filepath.ToSlash(filepath.Clean(root))

	var fullPath string
	var relPath string

	if filepath.IsAbs(candidate) || (len(candidate) >= 2 && candidate[1] == ':') {
		fullPath = cleanCandidate
		rel, err := filepath.Rel(cleanRoot, fullPath)
		if err != nil || strings.HasPrefix(filepath.ToSlash(rel), "..") {
			return "", false // Outside workspace root
		}
		relPath = filepath.ToSlash(rel)
	} else {
		// Relative path: must not start with ..
		if strings.HasPrefix(cleanCandidate, "..") {
			return "", false // Path traversal escape attempt
		}
		relPath = cleanCandidate
		fullPath = filepath.ToSlash(filepath.Join(cleanRoot, relPath))
	}

	relPath = filepath.ToSlash(filepath.Clean(relPath))
	if relPath == "." || relPath == "" || strings.HasPrefix(relPath, "..") {
		return "", false
	}

	return relPath, true
}

// InjectDynamicRoots parses package.json, mcp_registry.json, and hooks.json in root,
// creating virtual $root:manifest node and EdgeImports to valid entrypoint files.
func InjectDynamicRoots(root string, engine *Engine) error {
	cleanRoot := filepath.ToSlash(filepath.Clean(root))
	targetMap := make(map[string]bool)

	addTarget := func(candidate string) {
		relPath, ok := sanitizeAndValidatePath(cleanRoot, candidate)
		if !ok {
			return
		}

		// Verify target exists in engine nodes or on disk within root
		if _, exists := engine.GetNode(relPath); exists {
			targetMap[relPath] = true
			return
		}

		fullPath := filepath.Join(cleanRoot, relPath)
		if info, err := os.Stat(fullPath); err == nil && !info.IsDir() {
			targetMap[relPath] = true
		}
	}

	// 1. Parse package.json
	pkgPath := filepath.Join(cleanRoot, "package.json")
	if data, err := os.ReadFile(pkgPath); err == nil {
		var pkg packageJSONSchema
		if err := json.Unmarshal(data, &pkg); err == nil {
			if pkg.Main != "" {
				addTarget(pkg.Main)
			}
			switch b := pkg.Bin.(type) {
			case string:
				addTarget(b)
			case map[string]interface{}:
				for _, v := range b {
					if str, ok := v.(string); ok {
						addTarget(str)
					}
				}
			}
			for _, scriptCmd := range pkg.Scripts {
				for _, token := range extractCommandTokens(scriptCmd) {
					addTarget(token)
				}
			}
		}
	}

	// 2. Parse mcp_registry.json
	mcpPath := filepath.Join(cleanRoot, "mcp_registry.json")
	if data, err := os.ReadFile(mcpPath); err == nil {
		var mcpReg mcpRegistrySchema
		if err := json.Unmarshal(data, &mcpReg); err == nil {
			for _, m := range mcpReg.MCPs {
				for _, tok := range extractCommandTokens(m.Command) {
					addTarget(tok)
				}
				for _, arg := range m.Args {
					for _, tok := range extractCommandTokens(arg) {
						addTarget(tok)
					}
				}
			}
		}
	}

	// 3. Parse hooks.json
	hooksPath := filepath.Join(cleanRoot, "hooks.json")
	if data, err := os.ReadFile(hooksPath); err == nil {
		var genericHooks interface{}
		if err := json.Unmarshal(data, &genericHooks); err == nil {
			extractHooksCommands(genericHooks, addTarget)
		}
	}

	if len(targetMap) == 0 {
		return nil
	}

	// Register virtual root node
	vNode := Node{
		ID:       VirtualRootManifestID,
		Label:    "manifest_roots",
		FilePath: "",
		Type:     NodeFile,
	}
	_ = engine.AddNode(vNode)

	// Register EdgeImports to each target
	for target := range targetMap {
		_ = engine.AddEdge(Edge{
			From: VirtualRootManifestID,
			To:   target,
			Type: EdgeImports,
		})
	}

	return nil
}

func extractHooksCommands(v interface{}, callback func(string)) {
	switch val := v.(type) {
	case map[string]interface{}:
		for k, item := range val {
			if k == "command" {
				if cmdStr, ok := item.(string); ok {
					for _, tok := range extractCommandTokens(cmdStr) {
						callback(tok)
					}
				}
			} else {
				extractHooksCommands(item, callback)
			}
		}
	case []interface{}:
		for _, item := range val {
			extractHooksCommands(item, callback)
		}
	}
}
