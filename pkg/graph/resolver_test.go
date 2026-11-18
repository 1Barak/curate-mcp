package graph

import (
	"reflect"
	"testing"
)

// buildFixtureEngine creates a multi-module, multi-language simulated project graph
// with files, symbols, imports, and calls across Go, TypeScript, and Python.
// By default, only EdgeDefines edges are added, mirroring Phase 1 indexing.
func buildFixtureEngine() *Engine {
	engine := NewEngine()

	// -------------------------------------------------------------
	// 1. Go Module: cmd/app/main.go, pkg/auth/auth.go, pkg/crypto/hash.go
	// -------------------------------------------------------------
	// File: cmd/app/main.go
	_ = engine.AddNode(Node{
		ID:       "cmd/app/main.go",
		Label:    "main.go",
		FilePath: "cmd/app/main.go",
		Type:     NodeFile,
		Imports:  []string{"pkg/auth"},
	})
	// Symbol: cmd/app/main.go:main (calls exported Login)
	_ = engine.AddNode(Node{
		ID:       "cmd/app/main.go:main",
		Label:    "main",
		FilePath: "cmd/app/main.go",
		Type:     NodeSymbol,
		Imports:  []string{"pkg/auth"},
		Calls:    []string{"Login"},
	})
	_ = engine.AddEdge(Edge{From: "cmd/app/main.go", To: "cmd/app/main.go:main", Type: EdgeDefines})

	// File: pkg/auth/auth.go
	_ = engine.AddNode(Node{
		ID:       "pkg/auth/auth.go",
		Label:    "auth.go",
		FilePath: "pkg/auth/auth.go",
		Type:     NodeFile,
		Imports:  []string{"pkg/crypto"},
	})
	// Symbol: pkg/auth/auth.go:Login (exported, calls private validatePassword)
	_ = engine.AddNode(Node{
		ID:       "pkg/auth/auth.go:Login",
		Label:    "Login",
		FilePath: "pkg/auth/auth.go",
		Type:     NodeSymbol,
		Imports:  []string{"pkg/crypto"},
		Calls:    []string{"validatePassword"},
	})
	_ = engine.AddEdge(Edge{From: "pkg/auth/auth.go", To: "pkg/auth/auth.go:Login", Type: EdgeDefines})

	// Symbol: pkg/auth/auth.go:validatePassword (intra-file private, calls exported Hash)
	_ = engine.AddNode(Node{
		ID:       "pkg/auth/auth.go:validatePassword",
		Label:    "validatePassword",
		FilePath: "pkg/auth/auth.go",
		Type:     NodeSymbol,
		Imports:  []string{"pkg/crypto"},
		Calls:    []string{"Hash"},
	})
	_ = engine.AddEdge(Edge{From: "pkg/auth/auth.go", To: "pkg/auth/auth.go:validatePassword", Type: EdgeDefines})

	// Symbol: pkg/auth/auth.go:UnrelatedHelper (isolated definition, no calls)
	_ = engine.AddNode(Node{
		ID:       "pkg/auth/auth.go:UnrelatedHelper",
		Label:    "UnrelatedHelper",
		FilePath: "pkg/auth/auth.go",
		Type:     NodeSymbol,
	})
	_ = engine.AddEdge(Edge{From: "pkg/auth/auth.go", To: "pkg/auth/auth.go:UnrelatedHelper", Type: EdgeDefines})

	// File: pkg/crypto/hash.go
	_ = engine.AddNode(Node{
		ID:       "pkg/crypto/hash.go",
		Label:    "hash.go",
		FilePath: "pkg/crypto/hash.go",
		Type:     NodeFile,
		Imports:  []string{},
	})
	// Symbol: pkg/crypto/hash.go:Hash (exported, leaf function)
	_ = engine.AddNode(Node{
		ID:       "pkg/crypto/hash.go:Hash",
		Label:    "Hash",
		FilePath: "pkg/crypto/hash.go",
		Type:     NodeSymbol,
		Calls:    []string{},
	})
	_ = engine.AddEdge(Edge{From: "pkg/crypto/hash.go", To: "pkg/crypto/hash.go:Hash", Type: EdgeDefines})

	// -------------------------------------------------------------
	// 2. TypeScript Module: src/app.ts, src/auth.ts
	// -------------------------------------------------------------
	// File: src/app.ts
	_ = engine.AddNode(Node{
		ID:       "src/app.ts",
		Label:    "app.ts",
		FilePath: "src/app.ts",
		Type:     NodeFile,
		Imports:  []string{"./auth"},
	})
	// Symbol: src/app.ts:handleLogin (calls exported login)
	_ = engine.AddNode(Node{
		ID:       "src/app.ts:handleLogin",
		Label:    "handleLogin",
		FilePath: "src/app.ts",
		Type:     NodeSymbol,
		Imports:  []string{"./auth"},
		Calls:    []string{"login"},
	})
	_ = engine.AddEdge(Edge{From: "src/app.ts", To: "src/app.ts:handleLogin", Type: EdgeDefines})

	// File: src/auth.ts
	_ = engine.AddNode(Node{
		ID:       "src/auth.ts",
		Label:    "auth.ts",
		FilePath: "src/auth.ts",
		Type:     NodeFile,
		Imports:  []string{},
	})
	// Symbol: src/auth.ts:login (calls private validateToken)
	_ = engine.AddNode(Node{
		ID:       "src/auth.ts:login",
		Label:    "login",
		FilePath: "src/auth.ts",
		Type:     NodeSymbol,
		Calls:    []string{"validateToken"},
	})
	_ = engine.AddEdge(Edge{From: "src/auth.ts", To: "src/auth.ts:login", Type: EdgeDefines})

	// Symbol: src/auth.ts:validateToken (private leaf)
	_ = engine.AddNode(Node{
		ID:       "src/auth.ts:validateToken",
		Label:    "validateToken",
		FilePath: "src/auth.ts",
		Type:     NodeSymbol,
		Calls:    []string{},
	})
	_ = engine.AddEdge(Edge{From: "src/auth.ts", To: "src/auth.ts:validateToken", Type: EdgeDefines})

	// -------------------------------------------------------------
	// 3. Python Module: service/app.py, service/auth.py
	// -------------------------------------------------------------
	// File: service/app.py
	_ = engine.AddNode(Node{
		ID:       "service/app.py",
		Label:    "app.py",
		FilePath: "service/app.py",
		Type:     NodeFile,
		Imports:  []string{"service.auth"},
	})
	// Symbol: service/app.py:start (calls exported authenticate)
	_ = engine.AddNode(Node{
		ID:       "service/app.py:start",
		Label:    "start",
		FilePath: "service/app.py",
		Type:     NodeSymbol,
		Imports:  []string{"service.auth"},
		Calls:    []string{"authenticate"},
	})
	_ = engine.AddEdge(Edge{From: "service/app.py", To: "service/app.py:start", Type: EdgeDefines})

	// File: service/auth.py
	_ = engine.AddNode(Node{
		ID:       "service/auth.py",
		Label:    "auth.py",
		FilePath: "service/auth.py",
		Type:     NodeFile,
		Imports:  []string{},
	})
	// Symbol: service/auth.py:authenticate (calls private _verify_hash)
	_ = engine.AddNode(Node{
		ID:       "service/auth.py:authenticate",
		Label:    "authenticate",
		FilePath: "service/auth.py",
		Type:     NodeSymbol,
		Calls:    []string{"_verify_hash"},
	})
	_ = engine.AddEdge(Edge{From: "service/auth.py", To: "service/auth.py:authenticate", Type: EdgeDefines})

	// Symbol: service/auth.py:_verify_hash (private leaf)
	_ = engine.AddNode(Node{
		ID:       "service/auth.py:_verify_hash",
		Label:    "_verify_hash",
		FilePath: "service/auth.py",
		Type:     NodeSymbol,
		Calls:    []string{},
	})
	_ = engine.AddEdge(Edge{From: "service/auth.py", To: "service/auth.py:_verify_hash", Type: EdgeDefines})

	return engine
}

// TestCrossModuleGraphResolution_RedPhaseBaseline verifies the empirical defect described in SPEC-003 RF-3:
// Without the Phase 2 resolver, the initial index only connects EdgeDefines (File -> Symbol).
// As a result:
//   - edges_by_type.calls == 0
//   - edges_by_type.imports == 0
//   - find_path between main and validatePassword returns connected: false
//   - find_path between main and Login returns connected: false
// This test provides the empirical baseline proof of why Phase 2 is mandatory.
func TestCrossModuleGraphResolution_RedPhaseBaseline(t *testing.T) {
	engine := buildFixtureEngine()

	summary, err := engine.GetArchitectureSummary()
	if err != nil {
		t.Fatalf("failed to get architecture summary: %v", err)
	}

	// 1. Verify that before Phase 2 resolver, calls and imports are strictly 0
	if summary.EdgesByType[string(EdgeCalls)] != 0 {
		t.Errorf("baseline failure: expected edges_by_type.calls == 0 before resolver, got %d",
			summary.EdgesByType[string(EdgeCalls)])
	}
	if summary.EdgesByType[string(EdgeImports)] != 0 {
		t.Errorf("baseline failure: expected edges_by_type.imports == 0 before resolver, got %d",
			summary.EdgesByType[string(EdgeImports)])
	}
	if summary.EdgesByType[string(EdgeDefines)] <= 0 {
		t.Errorf("expected EdgeDefines > 0 in baseline, got %d",
			summary.EdgesByType[string(EdgeDefines)])
	}

	// 2. Verify that find_path cannot traverse across unlinked symbols
	pathRes, err := engine.FindShortestPath("cmd/app/main.go:main", "pkg/auth/auth.go:validatePassword")
	if err != nil {
		t.Fatalf("FindShortestPath returned error: %v", err)
	}
	if pathRes.Connected {
		t.Errorf("baseline failure: find_path reported connected=true without Phase 2 resolver: %v", pathRes.Path)
	}
	if len(pathRes.Path) != 0 {
		t.Errorf("expected empty path for disconnected nodes, got: %v", pathRes.Path)
	}

	// Cross-module entrypoint to exported function also disconnected
	entryRes, err := engine.FindShortestPath("cmd/app/main.go:main", "pkg/auth/auth.go:Login")
	if err != nil {
		t.Fatalf("FindShortestPath returned error: %v", err)
	}
	if entryRes.Connected {
		t.Errorf("baseline failure: find_path reported connected=true without calls edges: %v", entryRes.Path)
	}
}

// TestCrossModuleGraphResolution validates Acceptance Criteria 3 and RF-3:
// - Multi-module and multi-language scenario (Go, TypeScript, Python)
// - Generates EdgeImports (FileA -> FileB)
// - Generates EdgeCalls (caller -> callee) for both exported functions (Login) and private functions (validatePassword)
// - Validates edges_by_type.calls > 0 and edges_by_type.imports > 0
// - Validates find_path discovers legitimate execution path from entrypoint (main) to destination (validatePassword, Hash)
func TestCrossModuleGraphResolution(t *testing.T) {
	engine := buildFixtureEngine()

	// Execute Phase 2 cross-module edge resolution
	err := engine.ResolveCrossModuleEdges()
	if err != nil {
		t.Fatalf("ResolveCrossModuleEdges failed: %v", err)
	}

	summary, err := engine.GetArchitectureSummary()
	if err != nil {
		t.Fatalf("failed to get architecture summary: %v", err)
	}

	// 1. Metric verification: calls > 0 and imports > 0
	callsCount := summary.EdgesByType[string(EdgeCalls)]
	importsCount := summary.EdgesByType[string(EdgeImports)]

	if callsCount <= 0 {
		t.Errorf("RF-3 violation: expected edges_by_type.calls > 0, got %d", callsCount)
	}
	if importsCount <= 0 {
		t.Errorf("RF-3 violation: expected edges_by_type.imports > 0, got %d", importsCount)
	}

	// 2. Structural EdgeImports verification: FileA -> FileB
	expectedImports := [][2]string{
		{"cmd/app/main.go", "pkg/auth/auth.go"},
		{"pkg/auth/auth.go", "pkg/crypto/hash.go"},
		{"src/app.ts", "src/auth.ts"},
		{"service/app.py", "service/auth.py"},
	}
	for _, imp := range expectedImports {
		from, to := imp[0], imp[1]
		if !engine.HasEdge(from, to, EdgeImports) {
			t.Errorf("missing EdgeImports from %s -> %s", from, to)
		}
	}

	// 3. Structural EdgeCalls verification: symCaller -> symCallee
	expectedCalls := [][2]string{
		// Go: cross-module call to exported function
		{"cmd/app/main.go:main", "pkg/auth/auth.go:Login"},
		// Go: intra-file call to private unexported function
		{"pkg/auth/auth.go:Login", "pkg/auth/auth.go:validatePassword"},
		// Go: cross-module call from private function to crypto Hash
		{"pkg/auth/auth.go:validatePassword", "pkg/crypto/hash.go:Hash"},
		// TypeScript: cross-module call to exported function
		{"src/app.ts:handleLogin", "src/auth.ts:login"},
		// TypeScript: intra-file call to private function
		{"src/auth.ts:login", "src/auth.ts:validateToken"},
		// Python: cross-module call to exported function
		{"service/app.py:start", "service/auth.py:authenticate"},
		// Python: intra-file call to private function
		{"service/auth.py:authenticate", "service/auth.py:_verify_hash"},
	}
	for _, call := range expectedCalls {
		from, to := call[0], call[1]
		if !engine.HasEdge(from, to, EdgeCalls) {
			t.Errorf("missing EdgeCalls from %s -> %s", from, to)
		}
	}

	// 4. Functional path verification: find_path discovers execution path
	// Go 2-hop path: main -> Login -> validatePassword
	resGo2, err := engine.FindShortestPath("cmd/app/main.go:main", "pkg/auth/auth.go:validatePassword")
	if err != nil {
		t.Fatalf("FindShortestPath failed: %v", err)
	}
	if !resGo2.Connected {
		t.Errorf("expected connected=true between main and validatePassword")
	}
	if resGo2.Length != 2 {
		t.Errorf("expected path length=2, got %d", resGo2.Length)
	}
	expectedGo2Path := []string{
		"cmd/app/main.go:main",
		"pkg/auth/auth.go:Login",
		"pkg/auth/auth.go:validatePassword",
	}
	if !reflect.DeepEqual(resGo2.Path, expectedGo2Path) {
		t.Errorf("expected path %v, got %v", expectedGo2Path, resGo2.Path)
	}

	// Go 3-hop deep path: main -> Login -> validatePassword -> Hash
	resGo3, err := engine.FindShortestPath("cmd/app/main.go:main", "pkg/crypto/hash.go:Hash")
	if err != nil {
		t.Fatalf("FindShortestPath deep failed: %v", err)
	}
	if !resGo3.Connected {
		t.Errorf("expected connected=true between main and Hash")
	}
	if resGo3.Length != 3 {
		t.Errorf("expected path length=3, got %d", resGo3.Length)
	}
	expectedGo3Path := []string{
		"cmd/app/main.go:main",
		"pkg/auth/auth.go:Login",
		"pkg/auth/auth.go:validatePassword",
		"pkg/crypto/hash.go:Hash",
	}
	if !reflect.DeepEqual(resGo3.Path, expectedGo3Path) {
		t.Errorf("expected deep path %v, got %v", expectedGo3Path, resGo3.Path)
	}

	// TypeScript path: handleLogin -> login -> validateToken
	resTS, err := engine.FindShortestPath("src/app.ts:handleLogin", "src/auth.ts:validateToken")
	if err != nil {
		t.Fatalf("FindShortestPath TS failed: %v", err)
	}
	if !resTS.Connected {
		t.Errorf("expected connected=true for TypeScript path")
	}
	if resTS.Length != 2 {
		t.Errorf("expected TS path length=2, got %d", resTS.Length)
	}
	expectedTSPath := []string{
		"src/app.ts:handleLogin",
		"src/auth.ts:login",
		"src/auth.ts:validateToken",
	}
	if !reflect.DeepEqual(resTS.Path, expectedTSPath) {
		t.Errorf("expected TS path %v, got %v", expectedTSPath, resTS.Path)
	}

	// Python path: start -> authenticate -> _verify_hash
	resPy, err := engine.FindShortestPath("service/app.py:start", "service/auth.py:_verify_hash")
	if err != nil {
		t.Fatalf("FindShortestPath Python failed: %v", err)
	}
	if !resPy.Connected {
		t.Errorf("expected connected=true for Python path")
	}
	if resPy.Length != 2 {
		t.Errorf("expected Python path length=2, got %d", resPy.Length)
	}
	expectedPyPath := []string{
		"service/app.py:start",
		"service/auth.py:authenticate",
		"service/auth.py:_verify_hash",
	}
	if !reflect.DeepEqual(resPy.Path, expectedPyPath) {
		t.Errorf("expected Python path %v, got %v", expectedPyPath, resPy.Path)
	}
}

// TestCrossModuleGraphResolution_FalsacionActiva executes the mandatory Active Falsification Protocol
// (SPEC-035 / subproblem_falser) testing that removing any dependency legitimately breaks the path:
// 1. Removing intra-file private call (validatePassword) must cause find_path to return connected: false.
// 2. Removing cross-module call (Login) must cause find_path to return connected: false.
// 3. Removing import (pkg/auth) must prevent cross-module linking and cause find_path to return connected: false.
// 4. Verifying that sibling definitions under EdgeDefines never bridge disconnected symbols.
func TestCrossModuleGraphResolution_FalsacionActiva(t *testing.T) {
	// Subproblem Falser 1: Removing intra-file call from Login to validatePassword
	t.Run("Falsacion_RemoveIntraFilePrivateCall", func(t *testing.T) {
		engine := buildFixtureEngine()

		// Mutate Login node: remove validatePassword from Calls
		loginNode, ok := engine.GetNode("pkg/auth/auth.go:Login")
		if !ok {
			t.Fatalf("missing Login node")
		}
		loginNode.Calls = []string{} // severed!
		_ = engine.AddNode(loginNode)

		if err := engine.ResolveCrossModuleEdges(); err != nil {
			t.Fatalf("ResolveCrossModuleEdges failed: %v", err)
		}

		res, err := engine.FindShortestPath("cmd/app/main.go:main", "pkg/auth/auth.go:validatePassword")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Connected {
			t.Errorf("Falsacion failure: expected connected=false after severing intra-file call, got: %v", res.Path)
		}
		if len(res.Path) != 0 {
			t.Errorf("expected empty path on severed connection, got: %v", res.Path)
		}
	})

	// Subproblem Falser 2: Removing cross-module call from main to Login
	t.Run("Falsacion_RemoveCrossModuleCall", func(t *testing.T) {
		engine := buildFixtureEngine()

		// Mutate main node: remove Login from Calls
		mainNode, ok := engine.GetNode("cmd/app/main.go:main")
		if !ok {
			t.Fatalf("missing main node")
		}
		mainNode.Calls = []string{} // severed!
		_ = engine.AddNode(mainNode)

		if err := engine.ResolveCrossModuleEdges(); err != nil {
			t.Fatalf("ResolveCrossModuleEdges failed: %v", err)
		}

		res, err := engine.FindShortestPath("cmd/app/main.go:main", "pkg/auth/auth.go:validatePassword")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Connected {
			t.Errorf("Falsacion failure: expected connected=false after severing cross-module call, got: %v", res.Path)
		}
	})

	// Subproblem Falser 3: Removing import from cmd/app/main.go
	t.Run("Falsacion_RemoveModuleImport", func(t *testing.T) {
		engine := buildFixtureEngine()

		// Mutate main.go file node: remove pkg/auth from Imports
		fileNode, ok := engine.GetNode("cmd/app/main.go")
		if !ok {
			t.Fatalf("missing main.go file node")
		}
		fileNode.Imports = []string{} // severed import!
		_ = engine.AddNode(fileNode)

		// Also update symbol's imports metadata
		mainNode, _ := engine.GetNode("cmd/app/main.go:main")
		mainNode.Imports = []string{}
		_ = engine.AddNode(mainNode)

		if err := engine.ResolveCrossModuleEdges(); err != nil {
			t.Fatalf("ResolveCrossModuleEdges failed: %v", err)
		}

		res, err := engine.FindShortestPath("cmd/app/main.go:main", "pkg/auth/auth.go:validatePassword")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Connected {
			t.Errorf("Falsacion failure: expected connected=false when import is missing, got: %v", res.Path)
		}
	})

	// Subproblem Falser 4: Sibling definitions under EdgeDefines never bridge paths
	t.Run("Falsacion_DefinesEdgeIsolation", func(t *testing.T) {
		engine := buildFixtureEngine()

		if err := engine.ResolveCrossModuleEdges(); err != nil {
			t.Fatalf("ResolveCrossModuleEdges failed: %v", err)
		}

		// UnrelatedHelper is in pkg/auth/auth.go, defined by the same file as Login,
		// but nobody calls UnrelatedHelper. find_path must NOT bridge through auth.go!
		res, err := engine.FindShortestPath("cmd/app/main.go:main", "pkg/auth/auth.go:UnrelatedHelper")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Connected {
			t.Errorf("architectural violation: path was traversed across EdgeDefines to unrelated sibling: %v", res.Path)
		}
	})
}
