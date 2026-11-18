package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/1Barak/curate-mcp/pkg/graph"
)

// buildArchitectureFixtureEngine creates an in-memory graph with realistic files,
// symbols, imports, and calls that yields multiple high-centrality God Nodes
// for validating architecture summary formatting and bounds.
func buildArchitectureFixtureEngine() *graph.Engine {
	engine := graph.NewEngine()

	// Create 8 files and 16 symbols
	files := []string{
		"cmd/curate/main.go",
		"pkg/mcp/server.go",
		"pkg/mcp/receipts.go",
		"pkg/graph/engine.go",
		"pkg/graph/pruner.go",
		"pkg/graph/resolver.go",
		"pkg/parser/parser.go",
		"pkg/watcher/watcher.go",
	}

	for _, f := range files {
		_ = engine.AddNode(graph.Node{
			ID:       f,
			Label:    f,
			FilePath: f,
			Type:     graph.NodeFile,
		})
	}

	symbols := []struct {
		id       string
		label    string
		file     string
		inCalls  int
		outCalls int
	}{
		{"cmd/curate/main.go:main", "main", "cmd/curate/main.go", 0, 6},
		{"pkg/mcp/server.go:NewServer", "NewServer", "pkg/mcp/server.go", 5, 2},
		{"pkg/mcp/server.go:CurateSlice", "CurateSlice", "pkg/mcp/server.go", 4, 3},
		{"pkg/mcp/server.go:GetBlastRadius", "GetBlastRadius", "pkg/mcp/server.go", 4, 3},
		{"pkg/graph/engine.go:NewEngine", "NewEngine", "pkg/graph/engine.go", 6, 1},
		{"pkg/graph/engine.go:CalculateBlastRadius", "CalculateBlastRadius", "pkg/graph/engine.go", 7, 2},
		{"pkg/graph/engine.go:FindShortestPath", "FindShortestPath", "pkg/graph/engine.go", 4, 2},
		{"pkg/graph/pruner.go:PruneSlice", "PruneSlice", "pkg/graph/pruner.go", 3, 2},
		{"pkg/graph/resolver.go:ResolveCrossModuleEdges", "ResolveCrossModuleEdges", "pkg/graph/resolver.go", 3, 2},
		{"pkg/parser/parser.go:NewDispatcher", "NewDispatcher", "pkg/parser/parser.go", 2, 1},
		{"pkg/watcher/watcher.go:NewWatcher", "NewWatcher", "pkg/watcher/watcher.go", 2, 1},
	}

	for _, s := range symbols {
		_ = engine.AddNode(graph.Node{
			ID:       s.id,
			Label:    s.label,
			FilePath: s.file,
			Type:     graph.NodeSymbol,
		})
		_ = engine.AddEdge(graph.Edge{
			From: s.file,
			To:   s.id,
			Type: graph.EdgeDefines,
		})
	}

	// Create incoming call edges to generate high-degree God Nodes
	mainNode := "cmd/curate/main.go:main"
	targetSymbols := []string{
		"pkg/mcp/server.go:NewServer",
		"pkg/mcp/server.go:CurateSlice",
		"pkg/mcp/server.go:GetBlastRadius",
		"pkg/graph/engine.go:NewEngine",
		"pkg/graph/engine.go:CalculateBlastRadius",
		"pkg/graph/engine.go:FindShortestPath",
		"pkg/graph/pruner.go:PruneSlice",
		"pkg/graph/resolver.go:ResolveCrossModuleEdges",
	}

	for _, tgt := range targetSymbols {
		_ = engine.AddEdge(graph.Edge{
			From: mainNode,
			To:   tgt,
			Type: graph.EdgeCalls,
		})
	}

	// Cross-module calls to elevate in-degrees
	_ = engine.AddEdge(graph.Edge{From: "pkg/mcp/server.go:CurateSlice", To: "pkg/graph/engine.go:CalculateBlastRadius", Type: graph.EdgeCalls})
	_ = engine.AddEdge(graph.Edge{From: "pkg/mcp/server.go:CurateSlice", To: "pkg/graph/pruner.go:PruneSlice", Type: graph.EdgeCalls})
	_ = engine.AddEdge(graph.Edge{From: "pkg/mcp/server.go:GetBlastRadius", To: "pkg/graph/engine.go:CalculateBlastRadius", Type: graph.EdgeCalls})
	_ = engine.AddEdge(graph.Edge{From: "pkg/graph/resolver.go:ResolveCrossModuleEdges", To: "pkg/graph/engine.go:NewEngine", Type: graph.EdgeCalls})

	return engine
}

// ============================================================================
// RF-7: TestArchitectureSummary_DualFormat
// ============================================================================

func TestArchitectureSummary_DualFormat(t *testing.T) {
	server := NewServer(ServerConfig{
		WorkspaceRoot: "/workspace/curate-mcp",
		MaxTokens:     2000,
	})
	engine := buildArchitectureFixtureEngine()
	server.SetEngine(engine)

	ctx := context.Background()

	// ------------------------------------------------------------------------
	// Subtest 1: Baseline current server output exceeds 250 bytes (Red Phase Baseline)
	// ------------------------------------------------------------------------
	t.Run("BaselineCurrentServer_Exceeds250Bytes", func(t *testing.T) {
		res, err := server.GetArchitectureSummary(ctx)
		if err != nil {
			t.Fatalf("GetArchitectureSummary failed: %v", err)
		}
		rawBytes, err := json.Marshal(res)
		if err != nil {
			t.Fatalf("failed to marshal ArchitectureSummaryResponse: %v", err)
		}

		t.Logf("Baseline current raw ArchitectureSummaryResponse JSON size: %d bytes (payload: %s)", len(rawBytes), string(rawBytes))

		// Under current implementation, the full ArchitectureSummaryResponse with GodNodes
		// exceeds 250 bytes because GodNodes contain full node metadata.
		// RF-7 requires an ultralight structured representation <= 250 bytes.
		if len(rawBytes) <= 250 {
			t.Errorf("expected baseline unoptimized JSON to exceed 250 bytes, but was %d bytes", len(rawBytes))
		}
	})

	// ------------------------------------------------------------------------
	// Subtest 2: Formato 'json' (default cuando format es "" o "json") <= 250 bytes
	// ------------------------------------------------------------------------
	t.Run("DefaultFormat_EmptyString_UltralightJSON", func(t *testing.T) {
		resp, err := server.GetArchitectureSummaryFormatted(ctx, ArchitectureSummaryRequest{Format: ""})
		if err != nil {
			t.Fatalf("RF-7 violation: GetArchitectureSummaryFormatted failed for default format: %v", err)
		}
		if resp == nil {
			t.Fatal("expected non-nil ArchitectureSummaryFormattedResponse")
		}

		rawOutput := resp.Raw
		if rawOutput == "" && resp.JSON != nil {
			b, _ := json.Marshal(resp.JSON)
			rawOutput = string(b)
		}

		if len(rawOutput) == 0 {
			t.Fatal("RF-7 violation: output payload is empty")
		}

		if len(rawOutput) > 250 {
			t.Fatalf("RF-7 violation: default format output exceeds 250 bytes (got %d bytes): %s", len(rawOutput), rawOutput)
		}

		// Verify structured keys presence
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(rawOutput), &m); err != nil {
			t.Fatalf("RF-7 violation: default format must return valid JSON: %v (raw: %s)", err, rawOutput)
		}

		expectedKeys := []string{"status", "total_files", "total_symbols", "total_edges", "edges_by_type", "god_nodes"}
		for _, k := range expectedKeys {
			if _, ok := m[k]; !ok {
				t.Errorf("RF-7 violation: missing required JSON key %q in ultralight response: %s", k, rawOutput)
			}
		}
	})

	t.Run("ExplicitJSONFormat_UltralightJSON", func(t *testing.T) {
		resp, err := server.GetArchitectureSummaryFormatted(ctx, ArchitectureSummaryRequest{Format: "json"})
		if err != nil {
			t.Fatalf("RF-7 violation: GetArchitectureSummaryFormatted failed for format 'json': %v", err)
		}
		if resp == nil {
			t.Fatal("expected non-nil ArchitectureSummaryFormattedResponse")
		}

		rawOutput := resp.Raw
		if rawOutput == "" && resp.JSON != nil {
			b, _ := json.Marshal(resp.JSON)
			rawOutput = string(b)
		}

		if len(rawOutput) > 250 {
			t.Fatalf("RF-7 violation: explicit 'json' format exceeds 250 bytes (got %d bytes): %s", len(rawOutput), rawOutput)
		}

		var m map[string]interface{}
		if err := json.Unmarshal([]byte(rawOutput), &m); err != nil {
			t.Fatalf("RF-7 violation: format 'json' must return valid JSON: %v", err)
		}
	})

	// ------------------------------------------------------------------------
	// Subtest 3: Formato 'markdown' <= 4,096 bytes, métricas de salud, top 5 god nodes
	// ------------------------------------------------------------------------
	t.Run("MarkdownFormat_DelimitedReportAndBoundedGodNodes", func(t *testing.T) {
		resp, err := server.GetArchitectureSummaryFormatted(ctx, ArchitectureSummaryRequest{Format: "markdown"})
		if err != nil {
			t.Fatalf("RF-7 violation: GetArchitectureSummaryFormatted failed for format 'markdown': %v", err)
		}
		if resp == nil {
			t.Fatal("expected non-nil ArchitectureSummaryFormattedResponse")
		}

		md := resp.Markdown
		if md == "" && resp.Raw != "" {
			md = resp.Raw
		}

		if len(md) == 0 {
			t.Fatal("RF-7 violation: markdown output is empty")
		}

		// Strict delimiter bound <= 4,096 bytes
		if len(md) > 4096 {
			t.Fatalf("RF-7 violation: markdown report exceeds 4,096 bytes bound (got %d bytes)", len(md))
		}

		// Health and macro metrics verification
		if !strings.Contains(md, "Files") && !strings.Contains(md, "total_files") && !strings.Contains(md, "Archivos") {
			t.Errorf("RF-7 violation: markdown report missing files health metric: %s", md)
		}
		if !strings.Contains(md, "Symbols") && !strings.Contains(md, "total_symbols") && !strings.Contains(md, "Símbolos") {
			t.Errorf("RF-7 violation: markdown report missing symbols health metric: %s", md)
		}
		if !strings.Contains(md, "Edges") && !strings.Contains(md, "total_edges") && !strings.Contains(md, "Aristas") {
			t.Errorf("RF-7 violation: markdown report missing edges health metric: %s", md)
		}

		// God nodes section must exist and be strictly bounded to Top 5
		if !strings.Contains(strings.ToLower(md), "god node") {
			t.Errorf("RF-7 violation: markdown report missing God Nodes section: %s", md)
		}

		// Count occurrences of god node listings in markdown (must not exceed top 5)
		lines := strings.Split(md, "\n")
		godNodeCount := 0
		inGodNodesSection := false
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#") && strings.Contains(strings.ToLower(trimmed), "god node") {
				inGodNodesSection = true
				continue
			}
			if inGodNodesSection && strings.HasPrefix(trimmed, "#") {
				inGodNodesSection = false
			}
			if inGodNodesSection && (strings.HasPrefix(trimmed, "-") || strings.HasPrefix(trimmed, "*") || strings.HasPrefix(trimmed, "|")) {
				if strings.Contains(trimmed, ":") || strings.Contains(trimmed, "IC=") || strings.Contains(trimmed, "Ic=") {
					godNodeCount++
				}
			}
		}

		if godNodeCount > 5 {
			t.Errorf("RF-7 violation: markdown report includes %d God Nodes, exceeding strict Top 5 limit", godNodeCount)
		}

		// No destructive truncation: report must not end with a cut-off line
		lastLine := strings.TrimSpace(lines[len(lines)-1])
		if len(lastLine) > 0 && !strings.HasSuffix(lastLine, ".") && !strings.HasSuffix(lastLine, "|") && !strings.HasSuffix(lastLine, "`") && !strings.HasSuffix(lastLine, "*") && !strings.HasSuffix(lastLine, ")") {
			t.Logf("Notice: last line of markdown: %q", lastLine)
		}
	})

	// ------------------------------------------------------------------------
	// Subtest 4: Formato inválido ('xml', 'yaml') -> Error JSON-RPC -32602
	// ------------------------------------------------------------------------
	t.Run("InvalidFormat_XML_ReturnsRPC32602", func(t *testing.T) {
		resp, err := server.GetArchitectureSummaryFormatted(ctx, ArchitectureSummaryRequest{Format: "xml"})
		if err == nil {
			t.Fatalf("RF-7 violation: expected error for invalid format 'xml', got success: %+v", resp)
		}

		rpcErr, ok := err.(*RPCError)
		if !ok {
			// Also check error message
			if !strings.Contains(err.Error(), "-32602") && !strings.Contains(strings.ToLower(err.Error()), "invalid params") && !strings.Contains(strings.ToLower(err.Error()), "unsupported format") {
				t.Fatalf("RF-7 violation: expected RPCError with code -32602, got: %v", err)
			}
		} else {
			if rpcErr.Code != -32602 {
				t.Fatalf("RF-7 violation: expected RPC error code -32602, got %d", rpcErr.Code)
			}
		}
	})

	t.Run("InvalidFormat_YAML_ReturnsRPC32602", func(t *testing.T) {
		resp, err := server.GetArchitectureSummaryFormatted(ctx, ArchitectureSummaryRequest{Format: "yaml"})
		if err == nil {
			t.Fatalf("RF-7 violation: expected error for invalid format 'yaml', got success: %+v", resp)
		}

		rpcErr, ok := err.(*RPCError)
		if !ok || rpcErr.Code != -32602 {
			t.Fatalf("RF-7 violation: expected *RPCError with code -32602 for 'yaml', got: %v", err)
		}
	})
}

// ============================================================================
// RF-6: TestMemoryAutonomy_FreeOSMemory
// ============================================================================

func TestMemoryAutonomy_FreeOSMemory(t *testing.T) {
	// Injectable idle timeout for testing: 50 ms
	const testIdleTimeout = 50 * time.Millisecond

	var purgeCount int64
	purgeCallback := func() {
		atomic.AddInt64(&purgeCount, 1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	watcher := NewIdleWatcher(testIdleTimeout, purgeCallback)
	if watcher == nil {
		t.Fatal("expected non-nil IdleWatcher")
	}

	// ------------------------------------------------------------------------
	// Subtest 1: Single purge triggered after idle period
	// ------------------------------------------------------------------------
	t.Run("IdlePeriod_TriggersSinglePurge", func(t *testing.T) {
		atomic.StoreInt64(&purgeCount, 0)
		watcher.Start(ctx)

		// Wait for idle period to expire (50ms + 50ms margin)
		time.Sleep(100 * time.Millisecond)

		calls := atomic.LoadInt64(&purgeCount)
		if calls != 1 {
			t.Fatalf("RF-6 violation: expected exactly 1 purge invocation after idle timeout, got %d", calls)
		}

		if !watcher.IsPurged() {
			t.Fatalf("RF-6 violation: expected watcher.IsPurged() == true after idle purge")
		}
	})

	// ------------------------------------------------------------------------
	// Subtest 2: Prolonged idle does NOT continuously loop GC
	// ------------------------------------------------------------------------
	t.Run("ProlongedIdle_NoContinuousGCLoops", func(t *testing.T) {
		// Wait an additional 3x idle periods (150 ms) without activity
		time.Sleep(150 * time.Millisecond)

		calls := atomic.LoadInt64(&purgeCount)
		if calls != 1 {
			t.Fatalf("RF-6 violation: continuous GC loop detected! Expected purge count to remain 1, got %d", calls)
		}
	})

	// ------------------------------------------------------------------------
	// Subtest 3: New incoming activity resets idlePurged and allows subsequent purge
	// ------------------------------------------------------------------------
	t.Run("NewActivity_ResetsPurgedFlagAndAllowsSubsequentPurge", func(t *testing.T) {
		// Simulate incoming RPC activity
		watcher.RecordActivity()

		// idlePurged must reset immediately
		if watcher.IsPurged() {
			t.Fatalf("RF-6 violation: RecordActivity() must reset idlePurged to false")
		}

		// Wait for new idle period (100 ms)
		time.Sleep(100 * time.Millisecond)

		calls := atomic.LoadInt64(&purgeCount)
		if calls != 2 {
			t.Fatalf("RF-6 violation: expected second purge after activity reset + idle period, got %d (expected 2)", calls)
		}

		if !watcher.IsPurged() {
			t.Fatalf("RF-6 violation: expected watcher.IsPurged() == true after second purge")
		}
	})

	// ------------------------------------------------------------------------
	// Subtest 4: Stop / Context cancellation halts watcher
	// ------------------------------------------------------------------------
	t.Run("ContextCancellation_HaltsWatcher", func(t *testing.T) {
		watcher.Stop()

		time.Sleep(50 * time.Millisecond)
		watcher.RecordActivity()
		time.Sleep(100 * time.Millisecond)

		calls := atomic.LoadInt64(&purgeCount)
		if calls != 2 {
			t.Fatalf("RF-6 violation: watcher continued purging after Stop(): got %d calls (expected 2)", calls)
		}
	})
}

// ============================================================================
// SPEC-035: Falsación Activa Obligatoria (subproblem_falser)
// ============================================================================

func TestMemoryAutonomy_FalsacionActiva(t *testing.T) {
	// Subtest 1: Verify latch invariant - without activity, purge count never increments
	t.Run("Falsacion_NoActivity_StrictLatchHold", func(t *testing.T) {
		var purgeCount int64
		watcher := NewIdleWatcher(30*time.Millisecond, func() {
			atomic.AddInt64(&purgeCount, 1)
		})

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		watcher.Start(ctx)
		time.Sleep(70 * time.Millisecond)

		if atomic.LoadInt64(&purgeCount) != 1 {
			t.Fatalf("Falsación failure: expected initial purge count 1, got %d", atomic.LoadInt64(&purgeCount))
		}

		// Wait 150ms without RecordActivity()
		time.Sleep(150 * time.Millisecond)
		if atomic.LoadInt64(&purgeCount) != 1 {
			t.Fatalf("Falsación failure: latch violated! Purge re-triggered without activity: count=%d", atomic.LoadInt64(&purgeCount))
		}
	})

	// Subtest 2: Defensive nil callback handling
	t.Run("Falsacion_NilCallbackDefensiveHandling", func(t *testing.T) {
		watcher := NewIdleWatcher(20*time.Millisecond, nil)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Starting with nil callback must not panic
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Falsación failure: NewIdleWatcher with nil callback panicked: %v", r)
			}
		}()

		watcher.Start(ctx)
		time.Sleep(50 * time.Millisecond)
		watcher.RecordActivity()
		watcher.Stop()
	})
}

func TestArchitectureSummary_FalsacionActiva(t *testing.T) {
	server := NewServer(ServerConfig{
		WorkspaceRoot: "/workspace/curate-mcp",
		MaxTokens:     2000,
	})
	engine := buildArchitectureFixtureEngine()
	server.SetEngine(engine)

	ctx := context.Background()

	// Subtest 1: Random formats like protobuf/msgpack must return -32602
	t.Run("Falsacion_ArbitraryFormats_RejectedWithRPC32602", func(t *testing.T) {
		arbitraryFormats := []string{"protobuf", "msgpack", "bin", "csv", "html", "toml"}
		for _, fmtName := range arbitraryFormats {
			resp, err := server.GetArchitectureSummaryFormatted(ctx, ArchitectureSummaryRequest{Format: fmtName})
			if err == nil {
				t.Fatalf("Falsación failure: format %q must be rejected, got success: %+v", fmtName, resp)
			}
			rpcErr, ok := err.(*RPCError)
			if !ok || rpcErr.Code != -32602 {
				t.Fatalf("Falsación failure: expected *RPCError with code -32602 for format %q, got: %v", fmtName, err)
			}
		}
	})
}

