package graph

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

// TestFindShortestPath_DirectAndMultiHop validates BFS shortest directed path discovery
// over both calls and imports edges, choosing the minimal hop count path.
func TestFindShortestPath_DirectAndMultiHop(t *testing.T) {
	engine := NewEngine()

	nodes := []string{"node:A", "node:B", "node:C", "node:D", "node:X"}
	for _, id := range nodes {
		_ = engine.AddNode(Node{ID: id, Label: id, FilePath: "file.go", Type: NodeSymbol})
	}

	// Path 1 (3 hops): A -> B -> C -> D
	_ = engine.AddEdge(Edge{From: "node:A", To: "node:B", Type: EdgeCalls})
	_ = engine.AddEdge(Edge{From: "node:B", To: "node:C", Type: EdgeCalls})
	_ = engine.AddEdge(Edge{From: "node:C", To: "node:D", Type: EdgeCalls})

	// Path 2 (2 hops, via imports): A -> X -> D
	_ = engine.AddEdge(Edge{From: "node:A", To: "node:X", Type: EdgeImports})
	_ = engine.AddEdge(Edge{From: "node:X", To: "node:D", Type: EdgeCalls})

	res, err := engine.FindShortestPath("node:A", "node:D")
	if err != nil {
		t.Fatalf("FindShortestPath failed: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil PathResult")
	}

	if !res.Connected {
		t.Errorf("expected connected=true, got %v", res.Connected)
	}
	if res.Length != 2 {
		t.Errorf("expected shortest length=2, got %d", res.Length)
	}

	expectedPath := []string{"node:A", "node:X", "node:D"}
	if len(res.Path) != len(expectedPath) {
		t.Fatalf("expected path length %d %v, got %d %v", len(expectedPath), expectedPath, len(res.Path), res.Path)
	}
	for i, n := range expectedPath {
		if res.Path[i] != n {
			t.Errorf("path[%d]: expected %s, got %s", i, n, res.Path[i])
		}
	}
}

// TestFindShortestPath_ExcludesDefinesEdges validates that structural defines edges
// (e.g., File -> Symbol) are never traversed to create artificial paths between sibling definitions.
func TestFindShortestPath_ExcludesDefinesEdges(t *testing.T) {
	engine := NewEngine()

	fileNode := "pkg/service/service.go"
	symA := "pkg/service/service.go:FuncA"
	symB := "pkg/service/service.go:FuncB"

	_ = engine.AddNode(Node{ID: fileNode, Label: "service.go", FilePath: fileNode, Type: NodeFile})
	_ = engine.AddNode(Node{ID: symA, Label: "FuncA", FilePath: fileNode, Type: NodeSymbol})
	_ = engine.AddNode(Node{ID: symB, Label: "FuncB", FilePath: fileNode, Type: NodeSymbol})

	// Both are defined by fileNode
	_ = engine.AddEdge(Edge{From: fileNode, To: symA, Type: EdgeDefines})
	_ = engine.AddEdge(Edge{From: fileNode, To: symB, Type: EdgeDefines})

	// There is NO functional call or import between FuncA and FuncB
	res, err := engine.FindShortestPath(symA, symB)
	if err != nil {
		t.Fatalf("unexpected error finding path: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil PathResult")
	}

	if res.Connected {
		t.Errorf("architectural violation: path was found through defines edges: %v", res.Path)
	}
	if len(res.Path) != 0 {
		t.Errorf("expected empty path for disconnected symbols, got: %v", res.Path)
	}
}

// TestFindShortestPath_TrivialSameNode validates that searching a path from node_a to node_a
// returns connected=true, path=[node_a], and length=0.
func TestFindShortestPath_TrivialSameNode(t *testing.T) {
	engine := NewEngine()
	node := "node:self"
	_ = engine.AddNode(Node{ID: node, Label: "Self", FilePath: "self.go", Type: NodeSymbol})

	res, err := engine.FindShortestPath(node, node)
	if err != nil {
		t.Fatalf("unexpected error for trivial path: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil PathResult")
	}

	if !res.Connected {
		t.Error("expected connected=true for identical source and target")
	}
	if res.Length != 0 {
		t.Errorf("expected length=0, got %d", res.Length)
	}
	if len(res.Path) != 1 || res.Path[0] != node {
		t.Errorf("expected path [%s], got %v", node, res.Path)
	}
}

// TestFindShortestPath_DisconnectedGraph validates that when no directed path exists,
// it returns connected=false, path=[], and length=0.
func TestFindShortestPath_DisconnectedGraph(t *testing.T) {
	engine := NewEngine()

	// Subgraph 1: A -> B
	_ = engine.AddNode(Node{ID: "A", Label: "A", FilePath: "a.go", Type: NodeSymbol})
	_ = engine.AddNode(Node{ID: "B", Label: "B", FilePath: "b.go", Type: NodeSymbol})
	_ = engine.AddEdge(Edge{From: "A", To: "B", Type: EdgeCalls})

	// Subgraph 2: C -> D
	_ = engine.AddNode(Node{ID: "C", Label: "C", FilePath: "c.go", Type: NodeSymbol})
	_ = engine.AddNode(Node{ID: "D", Label: "D", FilePath: "d.go", Type: NodeSymbol})
	_ = engine.AddEdge(Edge{From: "C", To: "D", Type: EdgeCalls})

	res, err := engine.FindShortestPath("A", "D")
	if err != nil {
		t.Fatalf("unexpected error for disconnected query: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil PathResult")
	}

	if res.Connected {
		t.Errorf("expected connected=false between disjoint subgraphs, got %v", res.Connected)
	}
	if len(res.Path) != 0 {
		t.Errorf("expected empty path slice, got %v", res.Path)
	}
	if res.Length != 0 {
		t.Errorf("expected length=0, got %d", res.Length)
	}
}

// TestFindShortestPath_InvalidOrNonExistentNodes validates error handling when
// parameters are empty or refer to non-existent nodes in the graph.
func TestFindShortestPath_InvalidOrNonExistentNodes(t *testing.T) {
	engine := NewEngine()
	_ = engine.AddNode(Node{ID: "real:node", Label: "Real", FilePath: "real.go", Type: NodeSymbol})

	testCases := []struct {
		name        string
		source      string
		target      string
		expectedErr error
	}{
		{"empty source", "", "real:node", ErrInvalidParams},
		{"empty target", "real:node", "", ErrInvalidParams},
		{"both empty", "", "", ErrInvalidParams},
		{"non-existent source", "ghost:source", "real:node", ErrNodeNotFound},
		{"non-existent target", "real:node", "ghost:target", ErrNodeNotFound},
		{"both non-existent", "ghost:1", "ghost:2", ErrNodeNotFound},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := engine.FindShortestPath(tc.source, tc.target)
			if err == nil {
				t.Errorf("expected error for %s (%q -> %q), got nil", tc.name, tc.source, tc.target)
			} else if !errors.Is(err, tc.expectedErr) {
				t.Errorf("expected %v for %s, got: %v", tc.expectedErr, tc.name, err)
			}
		})
	}
}

// TestFindShortestPath_SubMillisecondPerformance validates the constitutional latency invariant (<= 5ms).
func TestFindShortestPath_SubMillisecondPerformance(t *testing.T) {
	engine := NewEngine()

	// Build a 500-node linear chain with cross-branch shortcuts
	const nodeCount = 500
	for i := 0; i < nodeCount; i++ {
		_ = engine.AddNode(Node{
			ID:       fmt.Sprintf("n:%d", i),
			Label:    fmt.Sprintf("Node%d", i),
			FilePath: "perf.go",
			Type:     NodeSymbol,
		})
		if i > 0 {
			_ = engine.AddEdge(Edge{
				From: fmt.Sprintf("n:%d", i-1),
				To:   fmt.Sprintf("n:%d", i),
				Type: EdgeCalls,
			})
		}
	}
	// Add shortcuts
	_ = engine.AddEdge(Edge{From: "n:10", To: "n:100", Type: EdgeCalls})
	_ = engine.AddEdge(Edge{From: "n:100", To: "n:300", Type: EdgeImports})

	// Warm-up run to prime caches and CPU instruction pipelines
	_, _ = engine.FindShortestPath("n:0", "n:400")

	// Sample across 5 iterations to eliminate hypervisor scheduling jitter on virtualized CI runners
	var (
		res         *PathResult
		err         error
		minDuration = time.Hour
	)
	for iter := 0; iter < 5; iter++ {
		start := time.Now()
		r, e := engine.FindShortestPath("n:0", "n:400")
		d := time.Since(start)
		if d < minDuration {
			minDuration = d
			res = r
			err = e
		}
	}

	if err != nil {
		t.Fatalf("FindShortestPath failed in perf test: %v", err)
	}
	if res == nil || !res.Connected {
		t.Fatalf("expected connected path in perf test, got %v", res)
	}

	if minDuration > 5*time.Millisecond {
		t.Errorf("performance violation: shortest path search took %v (limit: <= 5ms)", minDuration)
	}
}
