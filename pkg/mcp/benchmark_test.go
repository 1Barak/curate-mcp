package mcp_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/1Barak/curate-mcp/pkg/graph"
	"github.com/1Barak/curate-mcp/pkg/mcp"
)

func setupBenchmarkServer(numFiles, symbolsPerFile int) *mcp.Server {
	srv := mcp.NewServer(mcp.ServerConfig{
		WorkspaceRoot: ".",
		MaxTokens:     3500,
	})
	engine := graph.NewEngine()

	for f := 0; f < numFiles; f++ {
		filePath := fmt.Sprintf("pkg/module%d/service_%d.go", f/10, f)
		_ = engine.AddNode(graph.Node{
			ID:       filePath,
			Type:     graph.NodeFile,
			FilePath: filePath,
			Label:    fmt.Sprintf("service_%d.go", f),
		})

		for s := 0; s < symbolsPerFile; s++ {
			symID := fmt.Sprintf("%s:Func%d", filePath, s)
			_ = engine.AddNode(graph.Node{
				ID:       symID,
				Type:     graph.NodeSymbol,
				FilePath: filePath,
				Label:    fmt.Sprintf("Func%d", s),
				Imports:  []string{"github.com/org/repo/pkg/auth"},
			})
			_ = engine.AddEdge(graph.Edge{
				From: filePath,
				To:   symID,
				Type: graph.EdgeDefines,
			})
			if f > 0 {
				prevSymID := fmt.Sprintf("pkg/module%d/service_%d.go:Func%d", (f-1)/10, f-1, s)
				_ = engine.AddEdge(graph.Edge{
					From: symID,
					To:   prevSymID,
					Type: graph.EdgeCalls,
				})
			}
		}
	}

	srv.SetEngine(engine)
	return srv
}

func BenchmarkCurateSlice_1kNodes_MCP(b *testing.B) {
	srv := setupBenchmarkServer(200, 5)
	ctx := context.Background()
	req := mcp.CurateSliceRequest{
		Intent:   "service_50 Func2",
		MaxFiles: 5,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res, err := srv.CurateSlice(ctx, req)
		if err != nil {
			b.Fatal(err)
		}
		_ = res
	}
}

func BenchmarkCurateSlice_10kNodes_MCP(b *testing.B) {
	srv := setupBenchmarkServer(1000, 10)
	ctx := context.Background()
	req := mcp.CurateSliceRequest{
		Intent:   "service_500 Func5",
		MaxFiles: 5,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res, err := srv.CurateSlice(ctx, req)
		if err != nil {
			b.Fatal(err)
		}
		_ = res
	}
}

func BenchmarkCurateSlice_50kNodes_Monorepo_MCP(b *testing.B) {
	srv := setupBenchmarkServer(5000, 10)
	ctx := context.Background()
	req := mcp.CurateSliceRequest{
		Intent:   "service_2500 Func7",
		MaxFiles: 5,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res, err := srv.CurateSlice(ctx, req)
		if err != nil {
			b.Fatal(err)
		}
		_ = res
	}
}
