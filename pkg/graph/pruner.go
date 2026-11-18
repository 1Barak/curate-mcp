package graph

import (
	"encoding/json"
)

// Default and hard token budgets for agent context windows.
const (
	DefaultTokenBudget = 400
	HardCapTokens      = 1000
)

// CuratedSlice represents the extracted sub-graph ready for LLM consumption.
type CuratedSlice struct {
	Status          string   `json:"status"`
	Project         string   `json:"project"`
	Intent          string   `json:"intent"`
	PrimaryFiles    []string `json:"primary_files"`
	Entrypoints     []string `json:"entrypoints"`
	Symbols         []Node   `json:"symbols"`
	EstimatedTokens int      `json:"estimated_tokens"`
}

// EstimateTokens calculates estimated token count using ceil(len(json)/4).
func EstimateTokens(slice *CuratedSlice) int {
	data, err := json.Marshal(slice)
	if err != nil {
		return 0
	}
	return (len(data) + 3) / 4
}

// PruneSlice executes cascading deterministic pruning:
// symbols -> entrypoints -> primary_files
// ensuring estimated tokens <= maxTokens and never exceeding HardCapTokens (1,000).
func PruneSlice(slice *CuratedSlice, maxTokens int) (*CuratedSlice, error) {
	if slice == nil {
		return nil, nil
	}

	targetLimit := maxTokens
	if targetLimit > HardCapTokens {
		targetLimit = HardCapTokens
	}
	if targetLimit <= 0 {
		targetLimit = DefaultTokenBudget
	}

	cloned := &CuratedSlice{
		Status:       slice.Status,
		Project:      slice.Project,
		Intent:       slice.Intent,
		PrimaryFiles: append([]string(nil), slice.PrimaryFiles...),
		Entrypoints:  append([]string(nil), slice.Entrypoints...),
		Symbols:      append([]Node(nil), slice.Symbols...),
	}

	// 1. Prune symbols first
	for EstimateTokens(cloned) > targetLimit && len(cloned.Symbols) > 0 {
		cloned.Symbols = cloned.Symbols[:len(cloned.Symbols)-1]
	}

	// 2. Prune entrypoints next
	for EstimateTokens(cloned) > targetLimit && len(cloned.Entrypoints) > 0 {
		cloned.Entrypoints = cloned.Entrypoints[:len(cloned.Entrypoints)-1]
	}

	// 3. Prune primary files last
	for EstimateTokens(cloned) > targetLimit && len(cloned.PrimaryFiles) > 0 {
		cloned.PrimaryFiles = cloned.PrimaryFiles[:len(cloned.PrimaryFiles)-1]
	}

	cloned.EstimatedTokens = EstimateTokens(cloned)
	return cloned, nil
}
