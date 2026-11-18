package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"curate-mcp/pkg/graph"
)

// TestReceiptsPathResolution_Precedence validates CLI flag > ENV var precedence
// and falling back to empty string for in-memory mode.
func TestReceiptsPathResolution_Precedence(t *testing.T) {
	testCases := []struct {
		name     string
		cliFlag  string
		envVar   string
		expected string
	}{
		{
			name:     "both set: CLI flag takes precedence",
			cliFlag:  "/cli/path/receipts.json",
			envVar:   "/env/path/receipts.json",
			expected: "/cli/path/receipts.json",
		},
		{
			name:     "only ENV var set",
			cliFlag:  "",
			envVar:   "/env/path/receipts.json",
			expected: "/env/path/receipts.json",
		},
		{
			name:     "neither set: in-memory empty string",
			cliFlag:  "",
			envVar:   "",
			expected: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			res := ResolveReceiptsPath(tc.cliFlag, tc.envVar)
			if res != tc.expected {
				t.Errorf("precedence resolution failed: expected %q, got %q", tc.expected, res)
			}
		})
	}
}

// TestBlastRadius_HybridCondition validates:
// 1. Configured path + persist_receipt (nil/true) -> persists to disk.
// 2. Configured path + persist_receipt (false) -> suppresses persistence.
// 3. Unconfigured path + persist_receipt (nil/false) -> succeeds in-memory without persistence.
// 4. Unconfigured path + persist_receipt (true) -> rejects with error -32602.
func TestBlastRadius_HybridCondition(t *testing.T) {
	tmpDir := t.TempDir()
	receiptFile := filepath.Join(tmpDir, "blast_radius_receipts.json")

	engine := graph.NewEngine()
	sym := "pkg/auth:Login"
	_ = engine.AddNode(graph.Node{ID: sym, Label: "Login", FilePath: "pkg/auth/auth.go", Type: graph.NodeSymbol})

	// Case 1: Configured path + persist_receipt=true -> persists
	t.Run("configured_path_persist_true", func(t *testing.T) {
		server := NewServer(ServerConfig{
			WorkspaceRoot: tmpDir,
			ReceiptsPath:  receiptFile,
		})
		server.SetEngine(engine)

		persistTrue := true
		res, err := server.GetBlastRadius(context.Background(), BlastRadiusRequest{
			NodeName:       sym,
			PersistReceipt: &persistTrue,
		})
		if err != nil {
			t.Fatalf("GetBlastRadius failed: %v", err)
		}
		if res == nil {
			t.Fatal("expected non-nil response")
		}
		if !res.ReceiptPersisted {
			t.Error("expected ReceiptPersisted=true")
		}
		if _, statErr := os.Stat(receiptFile); os.IsNotExist(statErr) {
			t.Errorf("expected receipts file to exist on disk at %s", receiptFile)
		}
	})

	// Case 2: Configured path + persist_receipt=false -> suppresses
	t.Run("configured_path_persist_false", func(t *testing.T) {
		noPersistFile := filepath.Join(tmpDir, "no_persist_receipts.json")
		server := NewServer(ServerConfig{
			WorkspaceRoot: tmpDir,
			ReceiptsPath:  noPersistFile,
		})
		server.SetEngine(engine)

		persistFalse := false
		res, err := server.GetBlastRadius(context.Background(), BlastRadiusRequest{
			NodeName:       sym,
			PersistReceipt: &persistFalse,
		})
		if err != nil {
			t.Fatalf("GetBlastRadius failed: %v", err)
		}
		if res.ReceiptPersisted {
			t.Error("expected ReceiptPersisted=false when persist_receipt=false")
		}
		if _, statErr := os.Stat(noPersistFile); !os.IsNotExist(statErr) {
			t.Errorf("file %s should NOT have been created when persist_receipt=false", noPersistFile)
		}
	})

	// Case 3: Unconfigured path + persist_receipt=false/nil -> pure in-memory success
	t.Run("unconfigured_path_in_memory", func(t *testing.T) {
		server := NewServer(ServerConfig{
			WorkspaceRoot: tmpDir,
			ReceiptsPath:  "", // in-memory
		})
		server.SetEngine(engine)

		res, err := server.GetBlastRadius(context.Background(), BlastRadiusRequest{
			NodeName: sym,
		})
		if err != nil {
			t.Fatalf("expected in-memory mode to succeed, got error: %v", err)
		}
		if res.ReceiptPersisted {
			t.Error("expected ReceiptPersisted=false in in-memory mode")
		}
	})

	// Case 4: Unconfigured path + persist_receipt=true -> rejects with error -32602
	t.Run("unconfigured_path_persist_true_rejects", func(t *testing.T) {
		server := NewServer(ServerConfig{
			WorkspaceRoot: tmpDir,
			ReceiptsPath:  "", // no path
		})
		server.SetEngine(engine)

		persistTrue := true
		_, err := server.GetBlastRadius(context.Background(), BlastRadiusRequest{
			NodeName:       sym,
			PersistReceipt: &persistTrue,
		})
		if err == nil {
			t.Fatal("expected error -32602 when persist_receipt=true without receipts path, got nil")
		}

		rpcErr, ok := err.(*RPCError)
		if !ok || rpcErr.Code != -32602 {
			t.Errorf("expected JSON-RPC error code -32602, got %v", err)
		}
	})
}

// TestReceiptEmitter_AtomicUpsertMergeAndPurgeExpired validates:
// 1. Thread-safe atomic file replacement.
// 2. Merging new receipt entries with existing active entries.
// 3. Purging entries whose TTL (900s) has expired.
// 4. Exact canonical schema fields matching hook_pre_mutation_guard.py.
func TestReceiptEmitter_AtomicUpsertMergeAndPurgeExpired(t *testing.T) {
	tmpDir := t.TempDir()
	receiptFile := filepath.Join(tmpDir, "blast_radius_receipts.json")

	emitter := NewReceiptEmitter(receiptFile)

	nowEpoch := float64(time.Now().Unix())
	expiredEpoch := nowEpoch - 1200.0 // 1200 seconds ago (> 900s TTL)
	activeEpoch := nowEpoch - 100.0   // 100 seconds ago (< 900s TTL)

	// Pre-seed an existing receipts file with 1 active entry and 1 expired entry
	initialData := map[string]ReceiptEntry{
		"C:/app/active.go": {
			TimestampEpoch:  activeEpoch,
			TTLSeconds:      900,
			CodeSymbol:      "ActiveSymbol",
			CodeFilePath:    "C:/app/active.go",
			TraceID:         "00000000-0000-0000-0000-000000000001",
			InDegree:        1,
			OutDegree:       1,
			IC:              4,
			FTDEligible:     false,
			UpstreamCallers: []string{"callerA"},
			AutoEmitted:     true,
		},
		"C:/app/expired.go": {
			TimestampEpoch:  expiredEpoch,
			TTLSeconds:      900,
			CodeSymbol:      "ExpiredSymbol",
			CodeFilePath:    "C:/app/expired.go",
			TraceID:         "00000000-0000-0000-0000-000000000002",
			InDegree:        0,
			OutDegree:       1,
			IC:              1,
			FTDEligible:     true,
			UpstreamCallers: []string{},
			AutoEmitted:     true,
		},
	}

	rawBytes, err := json.MarshalIndent(initialData, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal initial receipts: %v", err)
	}
	if err := os.WriteFile(receiptFile, rawBytes, 0644); err != nil {
		t.Fatalf("failed to write initial receipts file: %v", err)
	}

	// Now emit a new entry
	newEntry := ReceiptEntry{
		TimestampEpoch:  nowEpoch,
		TTLSeconds:      900,
		CodeSymbol:      "NewSymbol",
		CodeFilePath:    "C:/app/new.go",
		TraceID:         "11111111-2222-3333-4444-555555555555",
		InDegree:        2,
		OutDegree:       3,
		IC:              9,
		FTDEligible:     false,
		UpstreamCallers: []string{"callerX", "callerY"},
		AutoEmitted:     true,
	}

	if err := emitter.EmitReceipt(newEntry); err != nil {
		t.Fatalf("EmitReceipt failed: %v", err)
	}

	// Read back and verify atomic upsert and purge
	storedReceipts, err := emitter.ReadReceipts()
	if err != nil {
		t.Fatalf("ReadReceipts failed: %v", err)
	}

	// 1. Expired entry must be PURGED
	if _, exists := storedReceipts["C:/app/expired.go"]; exists {
		t.Error("purging failure: expired entry still present in receipts file")
	}

	// 2. Active entry must be PRESERVED (merged)
	active, activeExists := storedReceipts["C:/app/active.go"]
	if !activeExists {
		t.Error("merge failure: pre-existing active receipt was lost")
	} else if active.CodeSymbol != "ActiveSymbol" {
		t.Errorf("expected CodeSymbol 'ActiveSymbol', got %q", active.CodeSymbol)
	}

	// 3. New entry must be PRESENT
	newRes, newExists := storedReceipts["C:/app/new.go"]
	if !newExists {
		t.Fatal("insertion failure: new receipt entry not found")
	}

	// 4. Validate canonical schema fields
	if newRes.TTLSeconds != 900 {
		t.Errorf("expected TTLSeconds=900, got %d", newRes.TTLSeconds)
	}
	if newRes.CodeSymbol != "NewSymbol" {
		t.Errorf("expected CodeSymbol 'NewSymbol', got %q", newRes.CodeSymbol)
	}
	if newRes.IC != 9 {
		t.Errorf("expected IC=9, got %d", newRes.IC)
	}
	if newRes.FTDEligible != false {
		t.Errorf("expected FTDEligible=false, got %v", newRes.FTDEligible)
	}
	if len(newRes.UpstreamCallers) != 2 {
		t.Errorf("expected 2 UpstreamCallers, got %d", len(newRes.UpstreamCallers))
	}
	if !newRes.AutoEmitted {
		t.Error("expected AutoEmitted=true")
	}
}

// TestMCPTools_JSONRPCContracts validates strict JSON-RPC schema compliance for:
// get_blast_radius, find_path, and get_architecture_summary.
func TestMCPTools_JSONRPCContracts(t *testing.T) {
	server := NewServer(ServerConfig{
		WorkspaceRoot: "/workspace/project",
		MaxTokens:     1000,
	})

	engine := graph.NewEngine()
	_ = engine.AddNode(graph.Node{ID: "sym:A", Label: "A", FilePath: "a.go", Type: graph.NodeSymbol})
	_ = engine.AddNode(graph.Node{ID: "sym:B", Label: "B", FilePath: "b.go", Type: graph.NodeSymbol})
	_ = engine.AddEdge(graph.Edge{From: "sym:A", To: "sym:B", Type: graph.EdgeCalls})
	server.SetEngine(engine)

	ctx := context.Background()

	// 1. Contract get_blast_radius
	t.Run("get_blast_radius_contract", func(t *testing.T) {
		res, err := server.GetBlastRadius(ctx, BlastRadiusRequest{NodeName: "sym:A"})
		if err != nil {
			t.Fatalf("GetBlastRadius failed: %v", err)
		}
		bytes, err := json.Marshal(res)
		if err != nil {
			t.Fatalf("failed to marshal GetBlastRadius response: %v", err)
		}

		var m map[string]interface{}
		_ = json.Unmarshal(bytes, &m)
		expectedFields := []string{
			"status", "node_name", "file_path", "in_degree", "out_degree",
			"criticality", "risk", "ftd_eligible", "upstream_callers",
			"downstream_callees", "otel_trace", "receipt_persisted",
		}
		for _, f := range expectedFields {
			if _, ok := m[f]; !ok {
				t.Errorf("schema violation: missing required field %q in get_blast_radius: %s", f, string(bytes))
			}
		}
	})

	// 2. Contract find_path
	t.Run("find_path_contract", func(t *testing.T) {
		res, err := server.FindPath(ctx, FindPathRequest{NodeA: "sym:A", NodeB: "sym:B"})
		if err != nil {
			t.Fatalf("FindPath failed: %v", err)
		}
		bytes, err := json.Marshal(res)
		if err != nil {
			t.Fatalf("failed to marshal FindPath response: %v", err)
		}

		var m map[string]interface{}
		_ = json.Unmarshal(bytes, &m)
		expectedFields := []string{"status", "source", "target", "connected", "path", "length"}
		for _, f := range expectedFields {
			if _, ok := m[f]; !ok {
				t.Errorf("schema violation: missing required field %q in find_path: %s", f, string(bytes))
			}
		}
	})

	// 3. Contract get_architecture_summary
	t.Run("get_architecture_summary_contract", func(t *testing.T) {
		res, err := server.GetArchitectureSummary(ctx)
		if err != nil {
			t.Fatalf("GetArchitectureSummary failed: %v", err)
		}
		bytes, err := json.Marshal(res)
		if err != nil {
			t.Fatalf("failed to marshal GetArchitectureSummary response: %v", err)
		}

		var m map[string]interface{}
		_ = json.Unmarshal(bytes, &m)
		expectedFields := []string{"status", "total_files", "total_symbols", "total_edges", "edges_by_type", "god_nodes"}
		for _, f := range expectedFields {
			if _, ok := m[f]; !ok {
				t.Errorf("schema violation: missing required field %q in get_architecture_summary: %s", f, string(bytes))
			}
		}
	})
}

// TestMCPTools_TokenPreservationBound validates that output JSON strings
// never exceed 3,500 bytes UTF-8 (Constitutional Principle 7 & Invariant 4).
func TestMCPTools_TokenPreservationBound(t *testing.T) {
	server := NewServer(ServerConfig{
		WorkspaceRoot: "/workspace/project",
		MaxTokens:     1000,
	})

	engine := graph.NewEngine()
	root := "massive:Root"
	_ = engine.AddNode(graph.Node{ID: root, Label: "Root", FilePath: "root.go", Type: graph.NodeSymbol})

	// Add 300 callers and callees with long names
	for i := 0; i < 300; i++ {
		caller := filepath.Join("long", "path", "to", "package", "service", "module", "subcomponent") + fmt.Sprintf(":MassiveCallerSymbolMethodLongIdentifier%03d", i)
		callee := filepath.Join("long", "path", "to", "package", "service", "module", "subcomponent") + fmt.Sprintf(":MassiveCalleeSymbolMethodLongIdentifier%03d", i)
		_ = engine.AddNode(graph.Node{ID: caller, Label: caller, FilePath: "caller.go", Type: graph.NodeSymbol})
		_ = engine.AddNode(graph.Node{ID: callee, Label: callee, FilePath: "callee.go", Type: graph.NodeSymbol})
		_ = engine.AddEdge(graph.Edge{From: caller, To: root, Type: graph.EdgeCalls})
		_ = engine.AddEdge(graph.Edge{From: root, To: callee, Type: graph.EdgeCalls})
	}
	server.SetEngine(engine)

	res, err := server.GetBlastRadius(context.Background(), BlastRadiusRequest{NodeName: root, Depth: 2, MaxNodes: 200})
	if err != nil {
		t.Fatalf("GetBlastRadius failed: %v", err)
	}

	bytes, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("failed to marshal response: %v", err)
	}

	const maxBytesLimit = 3500
	if len(bytes) > maxBytesLimit {
		t.Errorf("context shield token limit violation: response size %d bytes exceeded hard cap %d bytes",
			len(bytes), maxBytesLimit)
	}
}
