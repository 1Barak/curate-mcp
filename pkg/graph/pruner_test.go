package graph

import (
	"errors"
	"fmt"
	"runtime"
	"testing"
	"time"
)

// TestPruneSlice_Scalability validates RF-1:
//   - Pruning a synthetic slice with 3,000 nodes must complete in < 5 ms.
//   - Memory allocation must be negligible (< 5 MB).
//   - Semantic layers must be respected: Tier 1 (PrimaryFiles & Entrypoints) must be preserved
//     when budget allows, and only pruned as a last resort when Tier 1 alone exceeds budget.
func TestPruneSlice_Scalability(t *testing.T) {
	primaryFiles := []string{
		"pkg/service/auth.go",
		"pkg/service/user.go",
		"pkg/service/session.go",
		"pkg/service/router.go",
		"pkg/service/config.go",
	}

	entrypoints := []string{
		"cmd/api/main.go",
		"cmd/worker/main.go",
	}

	// 3,000 synthetic nodes to test O(N) linear pruning vs O(N^2) quadratic serialization loop
	symbols := make([]Node, 3000)
	for i := 0; i < 3000; i++ {
		symbols[i] = Node{
			ID:       fmt.Sprintf("pkg/service/comp_%d.go:SymbolFunc_%d", i%50, i),
			Label:    fmt.Sprintf("SymbolFunc_%d", i),
			FilePath: fmt.Sprintf("pkg/service/comp_%d.go", i%50),
			Type:     NodeSymbol,
			Imports:  []string{"github.com/org/repo/pkg/auth", "github.com/org/repo/pkg/db"},
		}
	}

	largeSlice := &CuratedSlice{
		Status:       "ok",
		Project:      "curate-mcp",
		Intent:       "scalability test with 3000 nodes",
		PrimaryFiles: primaryFiles,
		Entrypoints:  entrypoints,
		Symbols:      symbols,
	}

	// GC and baseline memory measurement
	runtime.GC()
	var m1, m2 runtime.MemStats
	runtime.ReadMemStats(&m1)

	start := time.Now()
	targetBudget := 2000
	pruned, err := PruneSlice(largeSlice, targetBudget)
	duration := time.Since(start)
	runtime.ReadMemStats(&m2)

	if err != nil {
		t.Fatalf("unexpected error during pruning: %v", err)
	}

	// 1. Latency Invariant: < 5 ms
	if duration >= 5*time.Millisecond {
		t.Errorf("scalability latency failure: PruneSlice took %v (invariant: < 5ms)", duration)
	}

	// 2. Memory Invariant: < 5 MB allocation
	allocMB := float64(m2.TotalAlloc-m1.TotalAlloc) / (1024 * 1024)
	if allocMB >= 5.0 {
		t.Errorf("scalability memory failure: PruneSlice allocated %.2f MB (invariant: < 5MB)", allocMB)
	}

	// 3. Semantic Layering Invariant:
	// Tier 1 (PrimaryFiles & Entrypoints) must be preserved when budget permits
	if len(pruned.PrimaryFiles) != len(primaryFiles) {
		t.Errorf("semantic layering failure: PrimaryFiles pruned prematurely (expected %d, got %d)",
			len(primaryFiles), len(pruned.PrimaryFiles))
	}
	if len(pruned.Entrypoints) != len(entrypoints) {
		t.Errorf("semantic layering failure: Entrypoints pruned prematurely (expected %d, got %d)",
			len(entrypoints), len(pruned.Entrypoints))
	}

	// Symbols must be pruned to fit budget
	if len(pruned.Symbols) >= len(symbols) {
		t.Errorf("symbols should have been pruned to fit budget (started with %d, pruned has %d)",
			len(symbols), len(pruned.Symbols))
	}

	// 4. Tier 1 Last Resort:
	// If budget is extremely small (e.g. 5 tokens), Tier 1 must be pruned as last resort
	tinyPruned, err := PruneSlice(largeSlice, 5)
	if err != nil {
		t.Fatalf("unexpected error during tiny budget pruning: %v", err)
	}
	if len(tinyPruned.PrimaryFiles) == len(primaryFiles) && len(tinyPruned.Entrypoints) == len(entrypoints) {
		t.Errorf("Tier 1 last resort failure: expected primary files or entrypoints to be pruned under extreme budget (5 tokens)")
	}
}

// TestAdaptiveBudgetAndPagination validates RF-2:
// - Dynamic budget selection according to max(files_tier, symbols_tier):
//   - (0, 0) -> 1000
//   - (99, 499) -> 1000
//   - (100, 10) -> 2000
//   - (20, 6000) -> 3500
//   - (1000, 5000) -> 2000
//   - Explicit max_tokens > 0 always prevails.
//   - Pagination metadata: has_more, total_matching_files, total_matching_symbols,
//     truncated_symbols_count, offset_files, next_offset_files.
//   - offset_files < 0 returns ErrInvalidOffset.
func TestAdaptiveBudgetAndPagination(t *testing.T) {
	// 1. Validate Adaptive Budget Calculation Formula
	budgetTests := []struct {
		name        string
		files       int
		symbols     int
		requested   int
		expectedTok int
	}{
		{"Small: (0, 0)", 0, 0, 0, 1000},
		{"Small upper edge: (99, 499)", 99, 499, 0, 1000},
		{"Medium by files: (100, 10)", 100, 10, 0, 2000},
		{"Large by symbols: (20, 6000)", 20, 6000, 0, 3500},
		{"Medium upper edge: (1000, 5000)", 1000, 5000, 0, 2000},
		{"Explicit max_tokens prevails small: 1500", 0, 0, 1500, 1500},
		{"Explicit max_tokens prevails high: 5000", 100, 10, 5000, 5000},
		{"Explicit max_tokens prevails low: 500", 20, 6000, 500, 500},
		{"Explicit max_tokens prevails medium: 3000", 1000, 5000, 3000, 3000},
	}

	for _, tc := range budgetTests {
		t.Run(tc.name, func(t *testing.T) {
			got := CalculateAdaptiveBudget(tc.files, tc.symbols, tc.requested)
			if got != tc.expectedTok {
				t.Errorf("CalculateAdaptiveBudget(%d, %d, %d) = %d; expected %d",
					tc.files, tc.symbols, tc.requested, got, tc.expectedTok)
			}
		})
	}

	// 2. Validate Pagination Metadata on CuratedSlice
	symbols := make([]Node, 100)
	for i := 0; i < 100; i++ {
		symbols[i] = Node{
			ID:       fmt.Sprintf("pkg/mod/file_%d.go:Sym%d", i%10, i),
			Label:    fmt.Sprintf("Sym%d", i),
			FilePath: fmt.Sprintf("pkg/mod/file_%d.go", i%10),
			Type:     NodeSymbol,
		}
	}

	primaryFiles := []string{
		"pkg/mod/file_0.go",
		"pkg/mod/file_1.go",
		"pkg/mod/file_2.go",
		"pkg/mod/file_3.go",
		"pkg/mod/file_4.go",
	}

	slice := &CuratedSlice{
		Status:                "ok",
		Project:               "curate-mcp",
		Intent:                "pagination test",
		PrimaryFiles:          primaryFiles,
		Entrypoints:           []string{"cmd/main.go"},
		Symbols:               symbols,
		TotalMatchingFiles:    20,
		TotalMatchingSymbols:  100,
		OffsetFiles:           0,
		NextOffsetFiles:       0,
		HasMore:               false,
		TruncatedSymbolsCount: 0,
	}

	// Request pruning with small budget to force truncation and pagination
	pruned, err := PruneSlice(slice, 300)
	if err != nil {
		t.Fatalf("unexpected error during pruning: %v", err)
	}

	// Validate metadata populated
	if pruned.TotalMatchingFiles != 20 {
		t.Errorf("expected TotalMatchingFiles=20, got %d", pruned.TotalMatchingFiles)
	}
	if pruned.TotalMatchingSymbols != 100 {
		t.Errorf("expected TotalMatchingSymbols=100, got %d", pruned.TotalMatchingSymbols)
	}
	if pruned.TruncatedSymbolsCount <= 0 {
		t.Errorf("expected TruncatedSymbolsCount > 0 due to budget pruning, got %d", pruned.TruncatedSymbolsCount)
	}
	if pruned.TruncatedSymbolsCount != (100 - len(pruned.Symbols)) {
		t.Errorf("expected TruncatedSymbolsCount=%d, got %d", 100-len(pruned.Symbols), pruned.TruncatedSymbolsCount)
	}
	if !pruned.HasMore {
		t.Errorf("expected HasMore=true when items are truncated")
	}
	if pruned.NextOffsetFiles <= 0 {
		t.Errorf("expected NextOffsetFiles > 0 for pagination continuation, got %d", pruned.NextOffsetFiles)
	}

	// 3. Validate negative offset error: offset_files < 0 must return ErrInvalidOffset
	invalidSlice := &CuratedSlice{
		Status:       "ok",
		Project:      "curate-mcp",
		Intent:       "negative offset test",
		PrimaryFiles: primaryFiles,
		Symbols:      symbols,
		OffsetFiles:  -1,
	}

	_, negErr := PruneSlice(invalidSlice, 1000)
	if negErr == nil {
		t.Fatalf("expected error when OffsetFiles < 0, got nil")
	}
	if !errors.Is(negErr, ErrInvalidOffset) {
		t.Errorf("expected error wrapping or equal to ErrInvalidOffset, got: %v", negErr)
	}
}
