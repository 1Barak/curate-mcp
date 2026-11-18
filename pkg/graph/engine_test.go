package graph

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
)

// TestEngine_ConcurrentInsertions verifies thread-safe concurrent insertion
// of nodes and edges under sync.RWMutex without data races or corruption.
func TestEngine_ConcurrentInsertions(t *testing.T) {
	engine := NewEngine()
	const workerCount = 50
	const itemsPerWorker = 20

	var wg sync.WaitGroup
	wg.Add(workerCount)

	for w := 0; w < workerCount; w++ {
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < itemsPerWorker; i++ {
				nodeID := fmt.Sprintf("node_%d_%d", workerID, i)
				node := Node{
					ID:       nodeID,
					Label:    fmt.Sprintf("SymbolWorker%dItem%d", workerID, i),
					FilePath: fmt.Sprintf("pkg/worker_%d/file_%d.go", workerID, i),
					Type:     NodeSymbol,
				}
				if err := engine.AddNode(node); err != nil {
					t.Errorf("unexpected error adding node: %v", err)
				}

				if i > 0 {
					prevID := fmt.Sprintf("node_%d_%d", workerID, i-1)
					edge := Edge{
						From: prevID,
						To:   nodeID,
						Type: EdgeCalls,
					}
					if err := engine.AddEdge(edge); err != nil {
						t.Errorf("unexpected error adding edge: %v", err)
					}
				}
			}
		}(w)
	}

	wg.Wait()

	expectedNodes := workerCount * itemsPerWorker
	if count := engine.NodeCount(); count != expectedNodes {
		t.Fatalf("concurrency assertion failure: expected %d nodes, got %d", expectedNodes, count)
	}

	expectedEdges := workerCount * (itemsPerWorker - 1)
	if edges := engine.EdgeCount(); edges != expectedEdges {
		t.Fatalf("concurrency assertion failure: expected %d edges, got %d", expectedEdges, edges)
	}
}

// TestNormalizeIntent_AlphanumericFiltering verifies normalization of intent query.
// Must lowercase, extract alphanumeric tokens >= 2 chars, and strip punctuation.
func TestNormalizeIntent_AlphanumericFiltering(t *testing.T) {
	tests := []struct {
		name     string
		rawQuery string
		expected []string
	}{
		{
			name:     "mixed case and punctuation",
			rawQuery: "Find Auth-Handler & JWT! tokens for v2_api?",
			expected: []string{"find", "auth", "handler", "jwt", "tokens", "for", "v2", "api"},
		},
		{
			name:     "single character filtering",
			rawQuery: "a b c auth x y service z",
			expected: []string{"auth", "service"},
		},
		{
			name:     "clean query",
			rawQuery: "user controller routes",
			expected: []string{"user", "controller", "routes"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tokens := NormalizeIntent(tc.rawQuery)
			if len(tokens) != len(tc.expected) {
				t.Fatalf("expected %d tokens %v, got %d tokens %v",
					len(tc.expected), tc.expected, len(tokens), tokens)
			}
			for i, token := range tokens {
				if token != tc.expected[i] {
					t.Errorf("at index %d: expected %q, got %q", i, tc.expected[i], token)
				}
			}
		})
	}
}

// TestScoring_MultiFieldWeighted verifies weighted scoring calculation:
// score = (label_matches * 2.0) + (file_path_matches * 1.5) + (import_matches * 1.0)
func TestScoring_MultiFieldWeighted(t *testing.T) {
	node := Node{
		ID:       "pkg/api/handlers/auth.go:AuthService",
		Label:    "AuthService",
		FilePath: "pkg/api/handlers/auth.go",
		Type:     NodeSymbol,
		Imports:  []string{"crypto/jwt", "net/http", "auth"},
	}

	// Query tokens: "auth", "service", "handlers", "jwt"
	// Label matches: "auth", "service" -> 2 matches * 2.0 = 4.0
	// FilePath matches: "handlers", "auth" -> 2 matches * 1.5 = 3.0
	// Imports matches: "jwt", "auth" -> 2 matches * 1.0 = 2.0
	// Expected total score = 4.0 + 3.0 + 2.0 = 9.0
	tokens := []string{"auth", "service", "handlers", "jwt"}
	expectedScore := 9.0

	score := CalculateScore(tokens, node)
	if score != expectedScore {
		t.Fatalf("scoring assertion failure: expected %f, got %f", expectedScore, score)
	}
}

// TestEntrypointDetection_InDegreeZeroAndPatterns verifies upstream entrypoint detection:
// In-degree 0 nodes and boundary patterns (routes, controllers, handlers, api, cli, main, server, index).
func TestEntrypointDetection_InDegreeZeroAndPatterns(t *testing.T) {
	engine := NewEngine()

	// Topología:
	// main.go (in-degree 0) -> cmd/cli/root.go (boundary: cli) -> api/routes.go (boundary: routes)
	// api/routes.go -> internal/services/auth.go (target) -> internal/models/user.go
	// worker/cron.go (in-degree 0, unrelated)
	nodes := []Node{
		{ID: "cmd/main.go", Label: "main", FilePath: "cmd/main.go", Type: NodeFile},
		{ID: "cmd/cli/root.go", Label: "RootCmd", FilePath: "cmd/cli/root.go", Type: NodeSymbol},
		{ID: "api/routes.go", Label: "RegisterRoutes", FilePath: "api/routes.go", Type: NodeFile},
		{ID: "internal/services/auth.go", Label: "AuthService", FilePath: "internal/services/auth.go", Type: NodeSymbol},
		{ID: "internal/models/user.go", Label: "User", FilePath: "internal/models/user.go", Type: NodeSymbol},
		{ID: "worker/cron.go", Label: "CronJob", FilePath: "worker/cron.go", Type: NodeFile},
	}

	for _, n := range nodes {
		if err := engine.AddNode(n); err != nil {
			t.Fatalf("failed to add node: %v", err)
		}
	}

	edges := []Edge{
		{From: "cmd/main.go", To: "cmd/cli/root.go", Type: EdgeCalls},
		{From: "cmd/cli/root.go", To: "api/routes.go", Type: EdgeCalls},
		{From: "api/routes.go", To: "internal/services/auth.go", Type: EdgeCalls},
		{From: "internal/services/auth.go", To: "internal/models/user.go", Type: EdgeCalls},
	}

	for _, e := range edges {
		if err := engine.AddEdge(e); err != nil {
			t.Fatalf("failed to add edge: %v", err)
		}
	}

	entrypoints := engine.FindEntrypoints([]string{"internal/services/auth.go"})

	if len(entrypoints) == 0 {
		t.Fatal("entrypoint detection failure: expected at least 1 entrypoint, got 0")
	}

	// Must include 1-hop predecessor boundary: api/routes.go
	foundRoute := false
	for _, ep := range entrypoints {
		if ep == "api/routes.go" {
			foundRoute = true
			break
		}
	}
	if !foundRoute {
		t.Errorf("entrypoint detection failure: expected 'api/routes.go' in entrypoints %v", entrypoints)
	}

	// Unrelated node worker/cron.go must NOT be an entrypoint for auth service
	for _, ep := range entrypoints {
		if ep == "worker/cron.go" {
			t.Errorf("entrypoint detection failure: unrelated node 'worker/cron.go' incorrectly identified as entrypoint")
		}
	}
}

// TestPruneSlice_DeterministicHardCap verifies deterministic cascading pruning:
// symbols -> entrypoints -> primary_files, and adherence to Hard Cap <= 1,000 tokens.
func TestPruneSlice_DeterministicHardCap(t *testing.T) {
	// Construct a large slice that easily exceeds 1,000 tokens
	var symbols []Node
	for i := 0; i < 60; i++ {
		symbols = append(symbols, Node{
			ID:       fmt.Sprintf("pkg/service/long_package_name/component_%d.go:VeryLongSymbolNameMethod%d", i, i),
			Label:    fmt.Sprintf("VeryLongSymbolNameMethod%d", i),
			FilePath: fmt.Sprintf("pkg/service/long_package_name/component_%d.go", i),
			Type:     NodeSymbol,
			Imports:  []string{"github.com/org/repo/pkg/auth", "github.com/org/repo/pkg/database"},
		})
	}

	var entrypoints []string
	for i := 0; i < 25; i++ {
		entrypoints = append(entrypoints, fmt.Sprintf("cmd/api/controllers/v1/auth_handler_endpoint_%d.go", i))
	}

	var primaryFiles []string
	for i := 0; i < 20; i++ {
		primaryFiles = append(primaryFiles, fmt.Sprintf("pkg/service/long_package_name/component_%d.go", i))
	}

	largeSlice := &CuratedSlice{
		Status:       "ok",
		Project:      "curate-mcp",
		Intent:       "authentication and session management in distributed microservices",
		PrimaryFiles: primaryFiles,
		Entrypoints:  entrypoints,
		Symbols:      symbols,
	}

	rawBytes, _ := json.Marshal(largeSlice)
	unprunedEstimatedTokens := (len(rawBytes) + 3) / 4

	if unprunedEstimatedTokens <= 1000 {
		t.Fatalf("test fixture precondition failure: unpruned tokens (%d) must exceed 1000", unprunedEstimatedTokens)
	}

	// 1. Request pruning with target maxTokens = 600
	pruned, err := PruneSlice(largeSlice, 600)
	if err != nil {
		t.Fatalf("unexpected error during pruning: %v", err)
	}

	prunedBytes, _ := json.Marshal(pruned)
	estimatedPrunedTokens := (len(prunedBytes) + 3) / 4

	if estimatedPrunedTokens > 600 {
		t.Fatalf("pruning assertion failure: estimated tokens %d exceeds requested max 600", estimatedPrunedTokens)
	}

	// Cascading order check: symbols must be pruned before primary files
	if len(pruned.Symbols) >= len(largeSlice.Symbols) {
		t.Errorf("cascading order failure: symbols should have been reduced (had %d, now %d)",
			len(largeSlice.Symbols), len(pruned.Symbols))
	}

	// 2. Request pruning with maxTokens exceeding HardCap (e.g. 2,500)
	// Must enforce Hard Cap <= 1,000 tokens unconditionally
	clampedPruned, err := PruneSlice(largeSlice, 2500)
	if err != nil {
		t.Fatalf("unexpected error during hard cap pruning: %v", err)
	}

	clampedBytes, _ := json.Marshal(clampedPruned)
	clampedTokens := (len(clampedBytes) + 3) / 4
	if clampedTokens > HardCapTokens {
		t.Fatalf("hard cap assertion failure: tokens %d exceeds HardCapTokens %d", clampedTokens, HardCapTokens)
	}
}
