package graph

import (
	"encoding/json"
	"errors"
)

// Default and hard token budgets for agent context windows according to RF-2.
const (
	DefaultTokenBudget = 1000
	HardCapTokens      = 3500
)

// ErrInvalidOffset is returned when offset_files is negative.
var ErrInvalidOffset = errors.New("pruner: offset_files cannot be negative")

// CuratedSlice represents the extracted sub-graph ready for LLM consumption.
type CuratedSlice struct {
	Status                string   `json:"status"`
	Project               string   `json:"project"`
	Intent                string   `json:"intent"`
	PrimaryFiles          []string `json:"primary_files"`
	Entrypoints           []string `json:"entrypoints"`
	Symbols               []Node   `json:"symbols"`
	EstimatedTokens       int      `json:"estimated_tokens"`
	HasMore               bool     `json:"has_more"`
	TotalMatchingFiles    int      `json:"total_matching_files"`
	TotalMatchingSymbols  int      `json:"total_matching_symbols"`
	TruncatedSymbolsCount int      `json:"truncated_symbols_count"`
	OffsetFiles           int      `json:"offset_files"`
	NextOffsetFiles       int      `json:"next_offset_files"`
}

// CalculateAdaptiveBudget calculates the dynamic token budget according to repository scale:
// max(nivel_archivos, nivel_simbolos):
//   - Small: files < 100 && symbols < 500 -> 1000
//   - Medium: (files >= 100 && files <= 1000) || (symbols >= 500 && symbols <= 5000), without exceeding Large -> 2000
//   - Large: files > 1000 || symbols > 5000 -> 3500
//
// If maxTokens > 0, explicit maxTokens always prevails.
func CalculateAdaptiveBudget(totalFiles, totalSymbols int, maxTokens int) int {
	if maxTokens > 0 {
		return maxTokens
	}

	tierFiles := 1
	if totalFiles > 1000 {
		tierFiles = 3
	} else if totalFiles >= 100 {
		tierFiles = 2
	}

	tierSymbols := 1
	if totalSymbols > 5000 {
		tierSymbols = 3
	} else if totalSymbols >= 500 {
		tierSymbols = 2
	}

	tier := tierFiles
	if tierSymbols > tier {
		tier = tierSymbols
	}

	switch tier {
	case 3:
		return 3500
	case 2:
		return 2000
	default:
		return 1000
	}
}

// EstimateTokens calculates estimated token count using ceil(len(json)/4).
func EstimateTokens(slice *CuratedSlice) int {
	data, err := json.Marshal(slice)
	if err != nil {
		return 0
	}
	return (len(data) + 3) / 4
}

func estimateBaseBytes(slice *CuratedSlice) int {
	return 270 + len(slice.Status) + len(slice.Project) + len(slice.Intent)
}

func estimateFileBytes(path string) int {
	return len(path) + 3
}

func estimateNodeBytes(n *Node) int {
	b := 45 + len(n.ID) + len(n.Label) + len(n.FilePath) + len(string(n.Type))
	if len(n.Imports) > 0 {
		b += 14
		for _, imp := range n.Imports {
			b += len(imp) + 3
		}
	}
	return b
}

// PruneSlice executes linear O(N) tiered semantic pruning:
// Tier 1 (PrimaryFiles & Entrypoints) is preserved whenever budget allows and only pruned
// as last resort when Tier 1 alone exceeds targetLimit.
// Symbols (Tier 2 to 4) are pruned in linear pass without quadratic serialization loops.
func PruneSlice(slice *CuratedSlice, maxTokens int) (*CuratedSlice, error) {
	if slice == nil {
		return nil, nil
	}
	if slice.OffsetFiles < 0 {
		return nil, ErrInvalidOffset
	}

	totalMatchingFiles := slice.TotalMatchingFiles
	if totalMatchingFiles == 0 {
		totalMatchingFiles = len(slice.PrimaryFiles)
	}
	totalMatchingSymbols := slice.TotalMatchingSymbols
	if totalMatchingSymbols == 0 {
		totalMatchingSymbols = len(slice.Symbols)
	}

	targetLimit := maxTokens
	if targetLimit <= 0 {
		targetLimit = CalculateAdaptiveBudget(totalMatchingFiles, totalMatchingSymbols, maxTokens)
	}

	byteBudget := targetLimit * 4

	cloned := &CuratedSlice{
		Status:               slice.Status,
		Project:              slice.Project,
		Intent:               slice.Intent,
		PrimaryFiles:         make([]string, 0, len(slice.PrimaryFiles)),
		Entrypoints:          make([]string, 0, len(slice.Entrypoints)),
		Symbols:              make([]Node, 0, len(slice.Symbols)),
		OffsetFiles:          slice.OffsetFiles,
		TotalMatchingFiles:   totalMatchingFiles,
		TotalMatchingSymbols: totalMatchingSymbols,
	}

	baseBytes := estimateBaseBytes(slice)

	primaryFilesBytes := 0
	for _, f := range slice.PrimaryFiles {
		primaryFilesBytes += estimateFileBytes(f)
	}
	entrypointsBytes := 0
	for _, ep := range slice.Entrypoints {
		entrypointsBytes += estimateFileBytes(ep)
	}

	tier1Bytes := baseBytes + primaryFilesBytes + entrypointsBytes

	if tier1Bytes <= byteBudget {
		cloned.PrimaryFiles = append(cloned.PrimaryFiles, slice.PrimaryFiles...)
		cloned.Entrypoints = append(cloned.Entrypoints, slice.Entrypoints...)

		remainingBytes := byteBudget - tier1Bytes
		currentSymbolBytes := 0
		for i := 0; i < len(slice.Symbols); i++ {
			nb := estimateNodeBytes(&slice.Symbols[i])
			if currentSymbolBytes+nb > remainingBytes {
				break
			}
			currentSymbolBytes += nb
			cloned.Symbols = append(cloned.Symbols, slice.Symbols[i])
		}
	} else {
		// Tier 1 alone exceeds budget: Prune Tier 1 as last resort.
		// Symbols are omitted (Tier 2, 3, 4 pruned first).
		currentBytes := baseBytes

		for _, f := range slice.PrimaryFiles {
			fb := estimateFileBytes(f)
			if currentBytes+fb > byteBudget {
				break
			}
			currentBytes += fb
			cloned.PrimaryFiles = append(cloned.PrimaryFiles, f)
		}

		for _, ep := range slice.Entrypoints {
			eb := estimateFileBytes(ep)
			if currentBytes+eb > byteBudget {
				break
			}
			currentBytes += eb
			cloned.Entrypoints = append(cloned.Entrypoints, ep)
		}
	}

	// Exact token validation and fine-tuning if needed
	cloned.EstimatedTokens = EstimateTokens(cloned)
	for cloned.EstimatedTokens > targetLimit && len(cloned.Symbols) > 0 {
		cloned.Symbols = cloned.Symbols[:len(cloned.Symbols)-1]
		cloned.EstimatedTokens = EstimateTokens(cloned)
	}
	for cloned.EstimatedTokens > targetLimit && len(cloned.Entrypoints) > 0 {
		cloned.Entrypoints = cloned.Entrypoints[:len(cloned.Entrypoints)-1]
		cloned.EstimatedTokens = EstimateTokens(cloned)
	}
	for cloned.EstimatedTokens > targetLimit && len(cloned.PrimaryFiles) > 0 {
		cloned.PrimaryFiles = cloned.PrimaryFiles[:len(cloned.PrimaryFiles)-1]
		cloned.EstimatedTokens = EstimateTokens(cloned)
	}

	// Calculate pagination and truncation metadata
	cloned.TruncatedSymbolsCount = cloned.TotalMatchingSymbols - len(cloned.Symbols)
	if cloned.TruncatedSymbolsCount < 0 {
		cloned.TruncatedSymbolsCount = 0
	}

	cloned.NextOffsetFiles = cloned.OffsetFiles + len(cloned.PrimaryFiles)
	cloned.HasMore = cloned.TruncatedSymbolsCount > 0 ||
		cloned.NextOffsetFiles < cloned.TotalMatchingFiles ||
		len(cloned.PrimaryFiles) < len(slice.PrimaryFiles) ||
		len(cloned.Entrypoints) < len(slice.Entrypoints)

	return cloned, nil
}
