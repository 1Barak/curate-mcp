package graph

import (
	"fmt"
	"testing"
)

// TestArchitectureSummary_ExactCounts validates exact aggregation of files, symbols,
// total edges, and edge counts partitioned by EdgeType.
func TestArchitectureSummary_ExactCounts(t *testing.T) {
	engine := NewEngine()

	// 3 Files
	_ = engine.AddNode(Node{ID: "f1.go", Label: "f1.go", FilePath: "f1.go", Type: NodeFile})
	_ = engine.AddNode(Node{ID: "f2.go", Label: "f2.go", FilePath: "f2.go", Type: NodeFile})
	_ = engine.AddNode(Node{ID: "f3.go", Label: "f3.go", FilePath: "f3.go", Type: NodeFile})

	// 5 Symbols
	symbols := []string{"sym:1", "sym:2", "sym:3", "sym:4", "sym:5"}
	for _, s := range symbols {
		_ = engine.AddNode(Node{ID: s, Label: s, FilePath: "f1.go", Type: NodeSymbol})
	}

	// 5 defines edges
	for _, s := range symbols {
		_ = engine.AddEdge(Edge{From: "f1.go", To: s, Type: EdgeDefines})
	}

	// 4 calls edges
	_ = engine.AddEdge(Edge{From: "sym:1", To: "sym:2", Type: EdgeCalls})
	_ = engine.AddEdge(Edge{From: "sym:2", To: "sym:3", Type: EdgeCalls})
	_ = engine.AddEdge(Edge{From: "sym:3", To: "sym:4", Type: EdgeCalls})
	_ = engine.AddEdge(Edge{From: "sym:4", To: "sym:5", Type: EdgeCalls})

	// 2 imports edges
	_ = engine.AddEdge(Edge{From: "sym:1", To: "sym:3", Type: EdgeImports})
	_ = engine.AddEdge(Edge{From: "sym:2", To: "sym:5", Type: EdgeImports})

	summary, err := engine.GetArchitectureSummary()
	if err != nil {
		t.Fatalf("GetArchitectureSummary failed: %v", err)
	}
	if summary == nil {
		t.Fatal("expected non-nil ArchitectureSummary")
	}

	if summary.TotalFiles != 3 {
		t.Errorf("expected TotalFiles=3, got %d", summary.TotalFiles)
	}
	if summary.TotalSymbols != 5 {
		t.Errorf("expected TotalSymbols=5, got %d", summary.TotalSymbols)
	}
	// Total edges = 5 defines + 4 calls + 2 imports = 11
	if summary.TotalEdges != 11 {
		t.Errorf("expected TotalEdges=11, got %d", summary.TotalEdges)
	}

	if summary.EdgesByType == nil {
		t.Fatal("expected non-nil EdgesByType map")
	}
	if summary.EdgesByType[string(EdgeDefines)] != 5 {
		t.Errorf("expected EdgesByType[defines]=5, got %d", summary.EdgesByType[string(EdgeDefines)])
	}
	if summary.EdgesByType[string(EdgeCalls)] != 4 {
		t.Errorf("expected EdgesByType[calls]=4, got %d", summary.EdgesByType[string(EdgeCalls)])
	}
	if summary.EdgesByType[string(EdgeImports)] != 2 {
		t.Errorf("expected EdgesByType[imports]=2, got %d", summary.EdgesByType[string(EdgeImports)])
	}
}

// TestArchitectureSummary_GodNodesExcludesFilesAndDefines validates that God Nodes:
// 1. Never include file nodes (only symbol nodes).
// 2. Never include defines edges in the degree metric (only functional calls & imports).
func TestArchitectureSummary_GodNodesExcludesFilesAndDefines(t *testing.T) {
	engine := NewEngine()

	// A huge file that defines 50 symbols
	megaFile := "huge_component.go"
	_ = engine.AddNode(Node{ID: megaFile, Label: megaFile, FilePath: megaFile, Type: NodeFile})

	for i := 0; i < 50; i++ {
		symID := fmt.Sprintf("sym:%d", i)
		_ = engine.AddNode(Node{ID: symID, Label: symID, FilePath: megaFile, Type: NodeSymbol})
		_ = engine.AddEdge(Edge{From: megaFile, To: symID, Type: EdgeDefines})
	}

	// Add a functional symbol with 2 functional calls
	functionalSym := "active_controller:Execute"
	_ = engine.AddNode(Node{ID: functionalSym, Label: "Execute", FilePath: megaFile, Type: NodeSymbol})
	_ = engine.AddEdge(Edge{From: functionalSym, To: "sym:0", Type: EdgeCalls})
	_ = engine.AddEdge(Edge{From: functionalSym, To: "sym:1", Type: EdgeImports})

	summary, err := engine.GetArchitectureSummary()
	if err != nil {
		t.Fatalf("GetArchitectureSummary failed: %v", err)
	}

	for _, gn := range summary.GodNodes {
		if gn.Name == megaFile {
			t.Errorf("architectural violation: file node %q appeared in GodNodes list", gn.Name)
		}
	}

	// Verify degree of active_controller:Execute
	var foundController *GodNode
	for i := range summary.GodNodes {
		if summary.GodNodes[i].Name == functionalSym || summary.GodNodes[i].Name == "Execute" {
			foundController = &summary.GodNodes[i]
			break
		}
	}
	if foundController == nil {
		t.Fatalf("expected functional symbol %q in GodNodes, got %v", functionalSym, summary.GodNodes)
	}

	if foundController.TotalDegree != 2 {
		t.Errorf("expected TotalDegree=2 (excluding defines), got %d", foundController.TotalDegree)
	}
}

// TestArchitectureSummary_GodNodesRankingAndDeterministicTieBreaking validates
// descending ordering by total functional degree (in + out) and alphabetical tie-breaking.
func TestArchitectureSummary_GodNodesRankingAndDeterministicTieBreaking(t *testing.T) {
	engine := NewEngine()

	// Create 4 symbols:
	// Zeta: in=2, out=4 (total=6) -> 1st
	// Alpha: in=2, out=2 (total=4) -> 2nd (tie with Beta, but 'Alpha' < 'Beta')
	// Beta: in=1, out=3 (total=4) -> 3rd
	// Delta: in=1, out=0 (total=1) -> 4th
	symbols := []string{"sym:Zeta", "sym:Alpha", "sym:Beta", "sym:Delta"}
	labels := []string{"Zeta", "Alpha", "Beta", "Delta"}
	for i, s := range symbols {
		_ = engine.AddNode(Node{ID: s, Label: labels[i], FilePath: "test.go", Type: NodeSymbol})
	}

	// Auxiliary nodes for edges
	for i := 0; i < 10; i++ {
		aux := fmt.Sprintf("aux:%d", i)
		_ = engine.AddNode(Node{ID: aux, Label: aux, FilePath: "test.go", Type: NodeSymbol})
	}

	// Zeta: in=2, out=4 (total=6)
	_ = engine.AddEdge(Edge{From: "aux:0", To: "sym:Zeta", Type: EdgeCalls})
	_ = engine.AddEdge(Edge{From: "aux:1", To: "sym:Zeta", Type: EdgeImports})
	_ = engine.AddEdge(Edge{From: "sym:Zeta", To: "aux:2", Type: EdgeCalls})
	_ = engine.AddEdge(Edge{From: "sym:Zeta", To: "aux:3", Type: EdgeCalls})
	_ = engine.AddEdge(Edge{From: "sym:Zeta", To: "aux:4", Type: EdgeImports})
	_ = engine.AddEdge(Edge{From: "sym:Zeta", To: "aux:5", Type: EdgeCalls})

	// Alpha: in=2, out=2 (total=4)
	_ = engine.AddEdge(Edge{From: "aux:0", To: "sym:Alpha", Type: EdgeCalls})
	_ = engine.AddEdge(Edge{From: "aux:1", To: "sym:Alpha", Type: EdgeImports})
	_ = engine.AddEdge(Edge{From: "sym:Alpha", To: "aux:2", Type: EdgeCalls})
	_ = engine.AddEdge(Edge{From: "sym:Alpha", To: "aux:3", Type: EdgeCalls})

	// Beta: in=1, out=3 (total=4)
	_ = engine.AddEdge(Edge{From: "aux:0", To: "sym:Beta", Type: EdgeCalls})
	_ = engine.AddEdge(Edge{From: "sym:Beta", To: "aux:1", Type: EdgeCalls})
	_ = engine.AddEdge(Edge{From: "sym:Beta", To: "aux:2", Type: EdgeCalls})
	_ = engine.AddEdge(Edge{From: "sym:Beta", To: "aux:3", Type: EdgeImports})

	// Delta: in=1, out=0 (total=1)
	_ = engine.AddEdge(Edge{From: "aux:0", To: "sym:Delta", Type: EdgeCalls})

	summary, err := engine.GetArchitectureSummary()
	if err != nil {
		t.Fatalf("GetArchitectureSummary failed: %v", err)
	}

	if len(summary.GodNodes) < 4 {
		t.Fatalf("expected at least 4 god nodes, got %d", len(summary.GodNodes))
	}

	// 1st: Zeta (total=6)
	if summary.GodNodes[0].TotalDegree != 6 {
		t.Errorf("1st god node expected degree 6, got %d (%s)",
			summary.GodNodes[0].TotalDegree, summary.GodNodes[0].Name)
	}

	// 2nd and 3rd must have degree 4
	if summary.GodNodes[1].TotalDegree != 4 || summary.GodNodes[2].TotalDegree != 4 {
		t.Errorf("2nd and 3rd expected degree 4, got %d and %d",
			summary.GodNodes[1].TotalDegree, summary.GodNodes[2].TotalDegree)
	}

	// Deterministic alphabetical tie-breaking: Alpha must precede Beta
	firstTied := summary.GodNodes[1].Name
	secondTied := summary.GodNodes[2].Name
	if firstTied > secondTied {
		t.Errorf("tie breaking failure: expected %q < %q, but got order [%s, %s]",
			firstTied, secondTied, firstTied, secondTied)
	}
}

// TestArchitectureSummary_GodNodesCappedAtTen validates that the top list never exceeds 10 entries.
func TestArchitectureSummary_GodNodesCappedAtTen(t *testing.T) {
	engine := NewEngine()

	// Create 20 symbols with descending degrees
	for i := 0; i < 20; i++ {
		sym := fmt.Sprintf("sym:%02d", i)
		_ = engine.AddNode(Node{ID: sym, Label: sym, FilePath: "file.go", Type: NodeSymbol})
		for j := 0; j < (20 - i); j++ {
			aux := fmt.Sprintf("aux:%02d_%02d", i, j)
			_ = engine.AddNode(Node{ID: aux, Label: aux, FilePath: "file.go", Type: NodeSymbol})
			_ = engine.AddEdge(Edge{From: sym, To: aux, Type: EdgeCalls})
		}
	}

	summary, err := engine.GetArchitectureSummary()
	if err != nil {
		t.Fatalf("GetArchitectureSummary failed: %v", err)
	}

	if len(summary.GodNodes) > 10 {
		t.Errorf("GodNodes list exceeded maximum cap of 10: got %d", len(summary.GodNodes))
	}
}
