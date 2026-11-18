package graph_test

import (
	"fmt"
	"testing"

	"github.com/1Barak/curate-mcp/pkg/graph"
)

// generateBenchmarkEngine builds a synthetic monorepo graph of given size
func generateBenchmarkEngine(numFiles, symbolsPerFile int) *graph.Engine {
	engine := graph.NewEngine()

	for f := 0; f < numFiles; f++ {
		filePath := fmt.Sprintf("pkg/module%d/service_%d.go", f/10, f)
		fileNode := graph.Node{
			ID:       filePath,
			Type:     graph.NodeFile,
			FilePath: filePath,
			Label:    fmt.Sprintf("service_%d.go", f),
		}
		_ = engine.AddNode(fileNode)

		for s := 0; s < symbolsPerFile; s++ {
			symID := fmt.Sprintf("%s:Func%d", filePath, s)
			symNode := graph.Node{
				ID:       symID,
				Type:     graph.NodeSymbol,
				FilePath: filePath,
				Label:    fmt.Sprintf("Func%d", s),
				Imports:  []string{"github.com/org/repo/pkg/auth", "github.com/org/repo/pkg/db"},
			}
			_ = engine.AddNode(symNode)
			_ = engine.AddEdge(graph.Edge{
				From: filePath,
				To:   symID,
				Type: graph.EdgeDefines,
			})

			// Add call edges to adjacent files to simulate a realistic dependency mesh
			if f > 0 {
				targetSymID := fmt.Sprintf("pkg/module%d/service_%d.go:Func%d", (f-1)/10, f-1, s)
				_ = engine.AddEdge(graph.Edge{
					From: symID,
					To:   targetSymID,
					Type: graph.EdgeCalls,
				})
			}
		}
	}
	return engine
}

// BenchmarkPruneSlice_3kNodes tests deterministic token pruning on 3,000 nodes
func BenchmarkPruneSlice_3kNodes(b *testing.B) {
	symbols := make([]graph.Node, 3000)
	for i := 0; i < 3000; i++ {
		symbols[i] = graph.Node{
			ID:       fmt.Sprintf("pkg/service/comp_%d.go:Symbol_%d", i%50, i),
			Label:    fmt.Sprintf("Symbol_%d", i),
			FilePath: fmt.Sprintf("pkg/service/comp_%d.go", i%50),
			Type:     graph.NodeSymbol,
			Imports:  []string{"github.com/org/repo/pkg/auth"},
		}
	}

	slice := &graph.CuratedSlice{
		Status:       "ok",
		Project:      "curate-mcp",
		Intent:       "benchmark pruning",
		PrimaryFiles: []string{"pkg/service/comp_1.go", "pkg/service/comp_2.go"},
		Entrypoints:  []string{"cmd/main.go"},
		Symbols:      symbols,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pruned, err := graph.PruneSlice(slice, 2000)
		if err != nil {
			b.Fatal(err)
		}
		_ = pruned
	}
}

// BenchmarkBlastRadius_1kNodes tests blast radius calculation on a 1k-node graph
func BenchmarkBlastRadius_1kNodes(b *testing.B) {
	engine := generateBenchmarkEngine(200, 5)
	target := "pkg/module10/service_100.go:Func2"
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		res, err := engine.CalculateBlastRadius(target, 2, 50)
		if err != nil {
			b.Fatal(err)
		}
		_ = res
	}
}

// BenchmarkBlastRadius_50kNodes_Monorepo tests BFS blast radius on a 50k-node monorepo
func BenchmarkBlastRadius_50kNodes_Monorepo(b *testing.B) {
	// 5,000 files * 10 symbols = 55,000 nodes, ~55,000 edges
	engine := generateBenchmarkEngine(5000, 10)
	target := "pkg/module250/service_2500.go:Func5"
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		res, err := engine.CalculateBlastRadius(target, 2, 50)
		if err != nil {
			b.Fatal(err)
		}
		_ = res
	}
}

// BenchmarkShortestPath_50kNodes_Monorepo tests BFS pathfinding across a 50k-node monorepo
func BenchmarkShortestPath_50kNodes_Monorepo(b *testing.B) {
	engine := generateBenchmarkEngine(5000, 10)
	src := "pkg/module10/service_105.go:Func3"
	dst := "pkg/module10/service_100.go:Func3"
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		res, err := engine.FindShortestPath(src, dst)
		if err != nil {
			b.Fatal(err)
		}
		_ = res
	}
}

// BenchmarkArchitectureSummary_50kNodes_Monorepo tests cardinality and God Nodes across 50k nodes
func BenchmarkArchitectureSummary_50kNodes_Monorepo(b *testing.B) {
	engine := generateBenchmarkEngine(5000, 10)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		summary, err := engine.GetArchitectureSummary()
		if err != nil {
			b.Fatal(err)
		}
		_ = summary
	}
}
