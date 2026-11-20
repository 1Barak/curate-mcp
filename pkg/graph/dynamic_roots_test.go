package graph

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDynamicRoots_ManifestInjection validates SPEC-004 RF-2:
// Injection of virtual root $root:manifest with EdgeImports to entrypoints found in
// package.json (bin, main, scripts), mcp_registry.json (command, args), and hooks.json (command).
func TestDynamicRoots_ManifestInjection(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Create fixtures on disk
	pkgJSON := `{
  "name": "test-project",
  "main": "dist/index.js",
  "bin": {
    "mycli": "./bin/cli.js"
  },
  "scripts": {
    "start": "node dist/index.js",
    "worker": "python -u workers/task.py"
  }
}`
	if err := os.WriteFile(filepath.Join(tempDir, "package.json"), []byte(pkgJSON), 0644); err != nil {
		t.Fatalf("failed to write package.json: %v", err)
	}

	mcpJSON := `{
  "mcps": {
    "my-server": {
      "command": "node",
      "args": ["servers/mcp.js"]
    }
  }
}`
	if err := os.WriteFile(filepath.Join(tempDir, "mcp_registry.json"), []byte(mcpJSON), 0644); err != nil {
		t.Fatalf("failed to write mcp_registry.json: %v", err)
	}

	hooksJSON := `{
  "PreInvocation": [
    {
      "command": "python scripts/hook.py"
    }
  ]
}`
	if err := os.WriteFile(filepath.Join(tempDir, "hooks.json"), []byte(hooksJSON), 0644); err != nil {
		t.Fatalf("failed to write hooks.json: %v", err)
	}

	// 2. Set up in-memory graph
	engine := NewEngine()

	entrypoints := []string{
		"dist/index.js",
		"bin/cli.js",
		"workers/task.py",
		"servers/mcp.js",
		"scripts/hook.py",
	}

	for _, ep := range entrypoints {
		_ = engine.AddNode(Node{
			ID:       ep,
			Label:    filepath.Base(ep),
			FilePath: ep,
			Type:     NodeFile,
		})
	}

	orphanFile := "pkg/orphan/unused.go"
	_ = engine.AddNode(Node{
		ID:       orphanFile,
		Label:    "unused.go",
		FilePath: orphanFile,
		Type:     NodeFile,
	})

	// 3. Execute Dynamic Roots injection
	if err := InjectDynamicRoots(tempDir, engine); err != nil {
		t.Fatalf("InjectDynamicRoots failed: %v", err)
	}

	// 4. Verify virtual root node existence
	vNode, exists := engine.GetNode(VirtualRootManifestID)
	if !exists {
		t.Fatalf("expected virtual root node %q to exist in engine", VirtualRootManifestID)
	}
	if vNode.FilePath != "" {
		t.Errorf("expected virtual root FilePath to be empty, got %q", vNode.FilePath)
	}

	// 5. Verify EdgeImports to all entrypoint files
	for _, ep := range entrypoints {
		if !engine.HasEdge(VirtualRootManifestID, ep, EdgeImports) {
			t.Errorf("missing virtual EdgeImports from %s -> %s", VirtualRootManifestID, ep)
		}
	}

	// 6. Verify orphan file does NOT receive virtual edge
	if engine.HasEdge(VirtualRootManifestID, orphanFile, EdgeImports) {
		t.Errorf("orphan file %s should NOT receive virtual EdgeImports", orphanFile)
	}
}

// TestDynamicRoots_PathTraversalDefense verifies that paths trying to escape the workspace
// (e.g. ../../etc/passwd or absolute external paths) are strictly rejected.
func TestDynamicRoots_PathTraversalDefense(t *testing.T) {
	tempDir := t.TempDir()

	pkgJSON := `{
  "main": "../../outside.js",
  "bin": "/absolute/outside.js",
  "scripts": {
    "evil": "node ../../escape.js"
  }
}`
	if err := os.WriteFile(filepath.Join(tempDir, "package.json"), []byte(pkgJSON), 0644); err != nil {
		t.Fatalf("failed to write package.json: %v", err)
	}

	engine := NewEngine()

	if err := InjectDynamicRoots(tempDir, engine); err != nil {
		t.Fatalf("InjectDynamicRoots returned error on traversal attempt: %v", err)
	}

	// Verify no edges were created from virtual root
	vNode, exists := engine.GetNode(VirtualRootManifestID)
	if exists {
		succs := engine.GetSuccessors(vNode.ID)
		if len(succs) > 0 {
			t.Errorf("expected zero successors for traversal escapes, got: %v", succs)
		}
	}
}

// TestDynamicRoots_MissingManifestsClean verifies that workspaces without manifests
// run cleanly without error or synthetic edges.
func TestDynamicRoots_MissingManifestsClean(t *testing.T) {
	tempDir := t.TempDir() // empty
	engine := NewEngine()

	if err := InjectDynamicRoots(tempDir, engine); err != nil {
		t.Fatalf("InjectDynamicRoots failed on empty dir: %v", err)
	}

	if _, exists := engine.GetNode(VirtualRootManifestID); exists {
		t.Errorf("did not expect virtual root to be created when no manifests exist")
	}
}
