package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/1Barak/curate-mcp/pkg/graph"
)

// RPCErrorPayload represents the wire JSON-RPC 2.0 error schema required by RF-4.
type RPCErrorPayload struct {
	Code    int           `json:"code"`
	Message string        `json:"message"`
	Data    *RPCErrorData `json:"data,omitempty"`
}

// RPCErrorData represents the actionable suggestions and hint in error data.
type RPCErrorData struct {
	Suggestions []string `json:"suggestions"`
	Hint        string   `json:"hint"`
}

// StructuralDiagnosticsPayload represents the structural diagnostics schema required by RF-5.
type StructuralDiagnosticsPayload struct {
	UnusedImports []string `json:"unused_imports"`
	DeadSymbols   []string `json:"dead_symbols"`
}

// parseErrorPayload marshals an error and parses the wire JSON-RPC error payload.
func parseErrorPayload(t *testing.T, err error) RPCErrorPayload {
	t.Helper()
	if err == nil {
		t.Fatal("expected non-nil error")
	}

	rawBytes, marshalErr := json.Marshal(err)
	if marshalErr != nil {
		t.Fatalf("failed to marshal error: %v", marshalErr)
	}

	var payload RPCErrorPayload
	if unmarshalErr := json.Unmarshal(rawBytes, &payload); unmarshalErr != nil {
		t.Fatalf("failed to unmarshal JSON-RPC error: %v (raw: %s)", unmarshalErr, string(rawBytes))
	}
	return payload
}

// parseDiagnosticsPayload extracts the structural_diagnostics section from a response JSON payload.
func parseDiagnosticsPayload(t *testing.T, raw []byte) *StructuralDiagnosticsPayload {
	t.Helper()
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("failed to unmarshal response envelope: %v", err)
	}

	diagRaw, exists := envelope["structural_diagnostics"]
	if !exists || len(diagRaw) == 0 || string(diagRaw) == "null" {
		return nil
	}

	var diag StructuralDiagnosticsPayload
	if err := json.Unmarshal(diagRaw, &diag); err != nil {
		t.Fatalf("failed to unmarshal structural_diagnostics: %v (raw: %s)", err, string(diagRaw))
	}
	return &diag
}

func assertContains(t *testing.T, list []string, target, fieldName string) {
	t.Helper()
	for _, item := range list {
		if item == target || strings.HasSuffix(item, target) {
			return
		}
	}
	t.Errorf("expected %s to contain %q, but got: %v", fieldName, target, list)
}

func assertNotContains(t *testing.T, list []string, target, fieldName string) {
	t.Helper()
	for _, item := range list {
		if item == target || strings.HasSuffix(item, target) {
			t.Errorf("expected %s NOT to contain %q, but found it in: %v", fieldName, target, list)
			return
		}
	}
}

func assertContainsSymbol(t *testing.T, list []string, fullID, simpleLabel, fieldName string) {
	t.Helper()
	for _, item := range list {
		if item == fullID || item == simpleLabel || strings.HasSuffix(item, ":"+simpleLabel) {
			return
		}
	}
	t.Errorf("expected %s to contain symbol %q (or %q), but got: %v", fieldName, fullID, simpleLabel, list)
}

func assertNotContainsSymbol(t *testing.T, list []string, fullID, simpleLabel, fieldName string) {
	t.Helper()
	for _, item := range list {
		if item == fullID || item == simpleLabel || strings.HasSuffix(item, ":"+simpleLabel) {
			t.Errorf("expected %s NOT to contain symbol %q (or %q), but found it in: %v", fieldName, fullID, simpleLabel, list)
			return
		}
	}
}

// TestFuzzySymbolResolution validates RF-4:
// - Query with simple name without path or prefix (e.g. 'LayaInferenceEngine') in get_blast_radius and find_path:
//   if a unique node with that suffix exists, it must auto-resolve returning the complete node.
// - Ambiguous suffix matches must NOT auto-resolve; they must return error -32602 with suggestions.
func TestFuzzySymbolResolution(t *testing.T) {
	server := NewServer(ServerConfig{
		WorkspaceRoot: "/workspace/curate-mcp",
	})
	engine := graph.NewEngine()

	// 1. Populate graph with unique symbols
	layaID := "infrastructure/laya-router-rs/src/inference.rs:LayaInferenceEngine"
	mainID := "cmd/curate/main.go:main"

	_ = engine.AddNode(graph.Node{
		ID:       "infrastructure/laya-router-rs/src/inference.rs",
		Label:    "inference.rs",
		FilePath: "infrastructure/laya-router-rs/src/inference.rs",
		Type:     graph.NodeFile,
	})
	_ = engine.AddNode(graph.Node{
		ID:       layaID,
		Label:    "LayaInferenceEngine",
		FilePath: "infrastructure/laya-router-rs/src/inference.rs",
		Type:     graph.NodeSymbol,
	})
	_ = engine.AddNode(graph.Node{
		ID:       "cmd/curate/main.go",
		Label:    "main.go",
		FilePath: "cmd/curate/main.go",
		Type:     graph.NodeFile,
	})
	_ = engine.AddNode(graph.Node{
		ID:       mainID,
		Label:    "main",
		FilePath: "cmd/curate/main.go",
		Type:     graph.NodeSymbol,
	})

	_ = engine.AddEdge(graph.Edge{From: "infrastructure/laya-router-rs/src/inference.rs", To: layaID, Type: graph.EdgeDefines})
	_ = engine.AddEdge(graph.Edge{From: "cmd/curate/main.go", To: mainID, Type: graph.EdgeDefines})
	_ = engine.AddEdge(graph.Edge{From: mainID, To: layaID, Type: graph.EdgeCalls})

	server.SetEngine(engine)

	// Subtest 1: get_blast_radius with simple name without path or prefix
	t.Run("GetBlastRadius_UniqueSuffix", func(t *testing.T) {
		res, err := server.GetBlastRadius(context.Background(), BlastRadiusRequest{
			NodeName: "LayaInferenceEngine",
		})
		if err != nil {
			t.Fatalf("expected fuzzy resolution to succeed for 'LayaInferenceEngine', got error: %v", err)
		}
		if res == nil {
			t.Fatal("expected non-nil BlastRadiusResponse")
		}
		if res.NodeName != layaID {
			t.Fatalf("expected resolved NodeName %q, got %q", layaID, res.NodeName)
		}
	})

	// Subtest 2: find_path with simple names without paths
	t.Run("FindPath_UniqueSuffix", func(t *testing.T) {
		res, err := server.FindPath(context.Background(), FindPathRequest{
			NodeA: "main",
			NodeB: "LayaInferenceEngine",
		})
		if err != nil {
			t.Fatalf("expected fuzzy resolution to succeed in find_path, got error: %v", err)
		}
		if res == nil {
			t.Fatal("expected non-nil FindPathResponse")
		}
		if !res.Connected {
			t.Errorf("expected connected path between main and LayaInferenceEngine")
		}
		if res.Source != mainID {
			t.Errorf("expected resolved Source %q, got %q", mainID, res.Source)
		}
		if res.Target != layaID {
			t.Errorf("expected resolved Target %q, got %q", layaID, res.Target)
		}
	})

	// Subtest 3: Ambiguous suffix must NOT auto-resolve; must return error with suggestions
	t.Run("AmbiguousSuffix_RejectsAutoResolution", func(t *testing.T) {
		ambig1 := "pkg/service/alpha.go:InitEngine"
		ambig2 := "pkg/service/beta.go:InitEngine"
		_ = engine.AddNode(graph.Node{ID: ambig1, Label: "InitEngine", FilePath: "pkg/service/alpha.go", Type: graph.NodeSymbol})
		_ = engine.AddNode(graph.Node{ID: ambig2, Label: "InitEngine", FilePath: "pkg/service/beta.go", Type: graph.NodeSymbol})

		res, err := server.GetBlastRadius(context.Background(), BlastRadiusRequest{
			NodeName: "InitEngine",
		})
		if err == nil {
			t.Fatalf("expected error for ambiguous symbol 'InitEngine', but got success with node: %v", res.NodeName)
		}
		payload := parseErrorPayload(t, err)
		if payload.Code != -32602 {
			t.Errorf("expected error code -32602 for ambiguous symbol, got %d", payload.Code)
		}
		if payload.Data == nil {
			t.Fatalf("expected data field in error for ambiguous symbol")
		}
		if len(payload.Data.Suggestions) < 2 {
			t.Errorf("expected at least 2 suggestions for ambiguous symbol, got %d", len(payload.Data.Suggestions))
		}
	})
}

// TestErrorSuggestionsAndActionableHint validates RF-4:
// - Query with non-existent symbol (e.g. 'LayaEngine' or 'NonExistent'): must return JSON-RPC error -32602.
// - data.suggestions must contain close matches (bounded to max 5, ordered by similarity).
// - data.hint must be contextualized with tool name ('get_blast_radius' or 'find_path').
func TestErrorSuggestionsAndActionableHint(t *testing.T) {
	server := NewServer(ServerConfig{
		WorkspaceRoot: "/workspace/curate-mcp",
	})
	engine := graph.NewEngine()

	layaID := "infrastructure/laya-router-rs/src/inference.rs:LayaInferenceEngine"
	_ = engine.AddNode(graph.Node{
		ID:       layaID,
		Label:    "LayaInferenceEngine",
		FilePath: "infrastructure/laya-router-rs/src/inference.rs",
		Type:     graph.NodeSymbol,
	})

	server.SetEngine(engine)

	t.Run("GetBlastRadius_NonExistent_SuggestionsAndHint", func(t *testing.T) {
		_, err := server.GetBlastRadius(context.Background(), BlastRadiusRequest{
			NodeName: "LayaEngine",
		})
		if err == nil {
			t.Fatal("expected error for non-existent symbol 'LayaEngine', got nil")
		}

		payload := parseErrorPayload(t, err)
		if payload.Code != -32602 {
			t.Errorf("expected error code -32602, got %d", payload.Code)
		}
		if payload.Data == nil {
			t.Fatalf("RF-4 violation: error response missing 'data' field (payload: %+v)", payload)
		}
		if len(payload.Data.Suggestions) == 0 {
			t.Fatalf("expected suggestions for 'LayaEngine' close match, got empty list")
		}
		if len(payload.Data.Suggestions) > 5 {
			t.Errorf("RF-4 violation: suggestions count %d exceeds maximum of 5", len(payload.Data.Suggestions))
		}

		hasLaya := false
		for _, s := range payload.Data.Suggestions {
			if s == layaID {
				hasLaya = true
				break
			}
		}
		if !hasLaya {
			t.Errorf("expected suggestions to contain %q, got: %v", layaID, payload.Data.Suggestions)
		}

		if !strings.Contains(payload.Data.Hint, "get_blast_radius") {
			t.Errorf("RF-4 violation: hint %q must mention tool name 'get_blast_radius'", payload.Data.Hint)
		}
	})

	t.Run("FindPath_NonExistent_HintContextualized", func(t *testing.T) {
		_, err := server.FindPath(context.Background(), FindPathRequest{
			NodeA: "NonExistentSource",
			NodeB: "LayaInferenceEngine",
		})
		if err == nil {
			t.Fatal("expected error for non-existent node in find_path, got nil")
		}

		payload := parseErrorPayload(t, err)
		if payload.Code != -32602 {
			t.Errorf("expected error code -32602, got %d", payload.Code)
		}
		if payload.Data == nil {
			t.Fatalf("RF-4 violation: error response missing 'data' field")
		}
		if !strings.Contains(payload.Data.Hint, "find_path") {
			t.Errorf("RF-4 violation: hint %q must mention tool name 'find_path'", payload.Data.Hint)
		}
	})

	t.Run("Suggestions_BoundedToMaxFive", func(t *testing.T) {
		boundedEngine := graph.NewEngine()
		for i := 1; i <= 8; i++ {
			id := fmt.Sprintf("pkg/workers/pool.go:WorkerAlpha%d", i)
			_ = boundedEngine.AddNode(graph.Node{
				ID:       id,
				Label:    fmt.Sprintf("WorkerAlpha%d", i),
				FilePath: "pkg/workers/pool.go",
				Type:     graph.NodeSymbol,
			})
		}
		boundedServer := NewServer(ServerConfig{WorkspaceRoot: "/workspace/test"})
		boundedServer.SetEngine(boundedEngine)

		_, err := boundedServer.GetBlastRadius(context.Background(), BlastRadiusRequest{
			NodeName: "WorkerAlpha",
		})
		if err == nil {
			t.Fatal("expected error for partial name 'WorkerAlpha', got nil")
		}

		payload := parseErrorPayload(t, err)
		if payload.Data == nil {
			t.Fatalf("RF-4 violation: error response missing 'data' field")
		}
		if len(payload.Data.Suggestions) != 5 {
			t.Errorf("RF-4 violation: suggestions count must be bounded to exactly 5, got %d (suggestions: %v)",
				len(payload.Data.Suggestions), payload.Data.Suggestions)
		}
	})

	t.Run("EmptySuggestions_CurateSliceFallbackHint", func(t *testing.T) {
		_, err := server.GetBlastRadius(context.Background(), BlastRadiusRequest{
			NodeName: "ZzZzZzTotallyUnknownSymbol999",
		})
		if err == nil {
			t.Fatal("expected error for completely unknown symbol, got nil")
		}

		payload := parseErrorPayload(t, err)
		if payload.Data == nil {
			t.Fatalf("RF-4 violation: error response missing 'data' field")
		}
		if len(payload.Data.Suggestions) != 0 {
			t.Errorf("expected 0 suggestions for unknown symbol, got %d", len(payload.Data.Suggestions))
		}
		if !strings.Contains(payload.Data.Hint, "curate_slice") {
			t.Errorf("RF-4 violation: hint %q must mention 'curate_slice' fallback when suggestions is empty", payload.Data.Hint)
		}
	})
}

// TestStructuralDiagnostics validates RF-5:
// - Response of curate_slice and get_blast_radius optionally includes 'structural_diagnostics'.
// - unused_imports: list of imports declared in file but not referenced by calls or symbols.
// - dead_symbols: list of private/unexported symbols with in_degree_calls == 0 (strictly EdgeCalls, excluding EdgeDefines).
// - Anti-false-positive: private symbol called by another symbol in the same file is NOT reported as dead.
// - Public symbols: exported symbols are NOT reported as dead even if in_degree_calls == 0.
func TestStructuralDiagnostics(t *testing.T) {
	server := NewServer(ServerConfig{
		WorkspaceRoot: "/workspace/curate-mcp",
	})
	engine := graph.NewEngine()

	// 1. Files
	authFile := "pkg/auth/service.go"
	cryptoFile := "pkg/crypto/hash.go"
	unusedImportFile := "pkg/legacy/deprecated.go"

	_ = engine.AddNode(graph.Node{
		ID:       authFile,
		Label:    "service.go",
		FilePath: authFile,
		Type:     graph.NodeFile,
		Imports:  []string{cryptoFile, unusedImportFile},
	})
	_ = engine.AddNode(graph.Node{
		ID:       cryptoFile,
		Label:    "hash.go",
		FilePath: cryptoFile,
		Type:     graph.NodeFile,
	})
	_ = engine.AddNode(graph.Node{
		ID:       unusedImportFile,
		Label:    "deprecated.go",
		FilePath: unusedImportFile,
		Type:     graph.NodeFile,
	})

	// 2. Symbols
	// Public exported symbol
	loginSym := "pkg/auth/service.go:Login"
	_ = engine.AddNode(graph.Node{
		ID:       loginSym,
		Label:    "Login",
		FilePath: authFile,
		Type:     graph.NodeSymbol,
		Calls:    []string{"validatePassword", "pkg/crypto/hash.go:Hash"},
	})

	// Private symbol called by Login (in_degree_calls == 1) -> NOT dead!
	validateSym := "pkg/auth/service.go:validatePassword"
	_ = engine.AddNode(graph.Node{
		ID:       validateSym,
		Label:    "validatePassword",
		FilePath: authFile,
		Type:     graph.NodeSymbol,
	})

	// Private symbol with NO incoming calls (in_degree_calls == 0) -> DEAD SYMBOL!
	deadSym := "pkg/auth/service.go:unusedPrivateHelper"
	_ = engine.AddNode(graph.Node{
		ID:       deadSym,
		Label:    "unusedPrivateHelper",
		FilePath: authFile,
		Type:     graph.NodeSymbol,
	})

	// External exported symbol called by Login
	hashSym := "pkg/crypto/hash.go:Hash"
	_ = engine.AddNode(graph.Node{
		ID:       hashSym,
		Label:    "Hash",
		FilePath: cryptoFile,
		Type:     graph.NodeSymbol,
	})

	// 3. Edges
	// EdgeDefines (MUST BE EXCLUDED from dead symbol in_degree_calls calculation!)
	_ = engine.AddEdge(graph.Edge{From: authFile, To: loginSym, Type: graph.EdgeDefines})
	_ = engine.AddEdge(graph.Edge{From: authFile, To: validateSym, Type: graph.EdgeDefines})
	_ = engine.AddEdge(graph.Edge{From: authFile, To: deadSym, Type: graph.EdgeDefines})
	_ = engine.AddEdge(graph.Edge{From: cryptoFile, To: hashSym, Type: graph.EdgeDefines})

	// EdgeImports
	_ = engine.AddEdge(graph.Edge{From: authFile, To: cryptoFile, Type: graph.EdgeImports})
	_ = engine.AddEdge(graph.Edge{From: authFile, To: unusedImportFile, Type: graph.EdgeImports})

	// EdgeCalls
	_ = engine.AddEdge(graph.Edge{From: loginSym, To: validateSym, Type: graph.EdgeCalls})
	_ = engine.AddEdge(graph.Edge{From: loginSym, To: hashSym, Type: graph.EdgeCalls})

	server.SetEngine(engine)

	t.Run("CurateSlice_IncludesStructuralDiagnostics", func(t *testing.T) {
		res, err := server.CurateSlice(context.Background(), CurateSliceRequest{
			Intent:   "service authentication login",
			MaxFiles: 5,
		})
		if err != nil {
			t.Fatalf("CurateSlice failed: %v", err)
		}
		if res == nil {
			t.Fatal("expected non-nil response")
		}

		rawBytes, err := json.Marshal(res)
		if err != nil {
			t.Fatalf("failed to marshal CurateSlice response: %v", err)
		}

		diag := parseDiagnosticsPayload(t, rawBytes)
		if diag == nil {
			t.Fatalf("RF-5 violation: curate_slice response missing 'structural_diagnostics' (payload: %s)", string(rawBytes))
		}

		// Verify unused_imports
		assertContains(t, diag.UnusedImports, unusedImportFile, "unused_imports")
		assertNotContains(t, diag.UnusedImports, cryptoFile, "unused_imports (should not include used import)")

		// Verify dead_symbols
		assertContainsSymbol(t, diag.DeadSymbols, deadSym, "unusedPrivateHelper", "dead_symbols")

		// Anti-false-positive: private symbol called internally MUST NOT be in dead_symbols
		assertNotContainsSymbol(t, diag.DeadSymbols, validateSym, "validatePassword", "dead_symbols (anti false positive)")

		// Exported symbol MUST NOT be in dead_symbols
		assertNotContainsSymbol(t, diag.DeadSymbols, loginSym, "Login", "dead_symbols (exported API)")
	})

	t.Run("GetBlastRadius_IncludesStructuralDiagnostics", func(t *testing.T) {
		res, err := server.GetBlastRadius(context.Background(), BlastRadiusRequest{
			NodeName: loginSym,
		})
		if err != nil {
			t.Fatalf("GetBlastRadius failed: %v", err)
		}
		if res == nil {
			t.Fatal("expected non-nil response")
		}

		rawBytes, err := json.Marshal(res)
		if err != nil {
			t.Fatalf("failed to marshal GetBlastRadius response: %v", err)
		}

		diag := parseDiagnosticsPayload(t, rawBytes)
		if diag == nil {
			t.Fatalf("RF-5 violation: get_blast_radius response missing 'structural_diagnostics' (payload: %s)", string(rawBytes))
		}

		assertContains(t, diag.UnusedImports, unusedImportFile, "unused_imports")
		assertContainsSymbol(t, diag.DeadSymbols, deadSym, "unusedPrivateHelper", "dead_symbols")
		assertNotContainsSymbol(t, diag.DeadSymbols, validateSym, "validatePassword", "dead_symbols (anti false positive)")
	})

	t.Run("FalsacionActiva_DeadSymbolWhenInternalCallRemoved", func(t *testing.T) {
		// Create a separate isolated engine where the call to validatePassword is removed
		falsedEngine := graph.NewEngine()
		_ = falsedEngine.AddNode(graph.Node{
			ID:       authFile,
			Label:    "service.go",
			FilePath: authFile,
			Type:     graph.NodeFile,
			Imports:  []string{cryptoFile},
		})
		_ = falsedEngine.AddNode(graph.Node{
			ID:       loginSym,
			Label:    "Login",
			FilePath: authFile,
			Type:     graph.NodeSymbol,
			Calls:    []string{"pkg/crypto/hash.go:Hash"}, // no validatePassword call!
		})
		_ = falsedEngine.AddNode(graph.Node{
			ID:       validateSym,
			Label:    "validatePassword",
			FilePath: authFile,
			Type:     graph.NodeSymbol,
		})
		_ = falsedEngine.AddNode(graph.Node{
			ID:       hashSym,
			Label:    "Hash",
			FilePath: cryptoFile,
			Type:     graph.NodeSymbol,
		})

		_ = falsedEngine.AddEdge(graph.Edge{From: authFile, To: loginSym, Type: graph.EdgeDefines})
		_ = falsedEngine.AddEdge(graph.Edge{From: authFile, To: validateSym, Type: graph.EdgeDefines})
		_ = falsedEngine.AddEdge(graph.Edge{From: loginSym, To: hashSym, Type: graph.EdgeCalls})
		// Note: NO EdgeCalls to validatePassword!

		falsedServer := NewServer(ServerConfig{WorkspaceRoot: "/workspace/test"})
		falsedServer.SetEngine(falsedEngine)

		res, err := falsedServer.GetBlastRadius(context.Background(), BlastRadiusRequest{
			NodeName: loginSym,
		})
		if err != nil {
			t.Fatalf("GetBlastRadius failed: %v", err)
		}

		rawBytes, err := json.Marshal(res)
		if err != nil {
			t.Fatalf("failed to marshal response: %v", err)
		}

		diag := parseDiagnosticsPayload(t, rawBytes)
		if diag == nil {
			t.Fatalf("RF-5 violation: get_blast_radius response missing 'structural_diagnostics'")
		}

		// NOW validatePassword MUST be detected as dead symbol!
		assertContainsSymbol(t, diag.DeadSymbols, validateSym, "validatePassword", "dead_symbols (now orphaned)")
	})
}
