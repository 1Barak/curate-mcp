package graph

import (
	"regexp"
	"strings"
)

// Scoring multi-field weights for ranking relevance.
const (
	WeightLabel    = 2.0
	WeightFilePath = 1.5
	WeightImports  = 1.0
)

// Standard entrypoint boundary patterns for web and CLI frameworks.
var BoundaryPatterns = []string{
	"routes",
	"controllers",
	"handlers",
	"api",
	"cli",
	"main",
	"server",
	"index",
}

var tokenRegex = regexp.MustCompile(`[a-zA-Z0-9]+`)

// NormalizeIntent normalizes the query string into lowercase alphanumeric tokens
// of length >= 2 characters, stripping punctuation.
func NormalizeIntent(raw string) []string {
	matches := tokenRegex.FindAllString(raw, -1)
	if len(matches) == 0 {
		return nil
	}

	tokens := make([]string, 0, len(matches))
	for _, m := range matches {
		t := strings.ToLower(m)
		if len(t) >= 2 {
			tokens = append(tokens, t)
		}
	}
	return tokens
}

// CalculateScore computes the weighted relevance score for a node against query tokens:
// Score = (label_matches * 2.0) + (file_path_matches * 1.5) + (import_matches * 1.0)
func CalculateScore(tokens []string, node Node) float64 {
	if len(tokens) == 0 {
		return 0.0
	}

	normLabel := strings.ToLower(node.Label)
	normPath := strings.ToLower(node.FilePath)

	var labelMatches, filePathMatches, importMatches int

	for _, token := range tokens {
		if strings.Contains(normLabel, token) {
			labelMatches++
		}
		if strings.Contains(normPath, token) {
			filePathMatches++
		}

		matchedImport := false
		for _, imp := range node.Imports {
			if strings.Contains(strings.ToLower(imp), token) {
				matchedImport = true
				break
			}
		}
		if matchedImport {
			importMatches++
		}
	}

	return (float64(labelMatches) * WeightLabel) +
		(float64(filePathMatches) * WeightFilePath) +
		(float64(importMatches) * WeightImports)
}

func isBoundary(path, label string) bool {
	lowerPath := strings.ToLower(path)
	lowerLabel := strings.ToLower(label)
	for _, pattern := range BoundaryPatterns {
		if strings.Contains(lowerPath, pattern) || strings.Contains(lowerLabel, pattern) {
			return true
		}
	}
	return false
}

// FindEntrypoints discovers upstream entrypoints for given seed node IDs within 1-hop:
// Nodes with in-degree 0 or matching boundary patterns (routes, controllers, handlers, etc.)
func (e *Engine) FindEntrypoints(seedNodeIDs []string) []string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	entrypointSet := make(map[string]bool)
	var result []string

	addEntrypoint := func(id string) {
		if strings.HasPrefix(id, "$root:") {
			return
		}
		if !entrypointSet[id] {
			entrypointSet[id] = true
			result = append(result, id)
		}
	}

	for _, seedID := range seedNodeIDs {
		preds := e.predecessors[seedID]
		for _, predID := range preds {
			predNode, exists := e.nodes[predID]
			path := predID
			label := ""
			if exists {
				if predNode.FilePath != "" {
					path = predNode.FilePath
				}
				label = predNode.Label
			}

			inDegree := len(e.predecessors[predID])
			if inDegree == 0 || isBoundary(path, label) {
				addEntrypoint(predID)
			}
		}

		// Check seed node itself if in-degree is 0 or matches boundary
		if len(preds) == 0 {
			seedNode, exists := e.nodes[seedID]
			path := seedID
			label := ""
			if exists {
				if seedNode.FilePath != "" {
					path = seedNode.FilePath
				}
				label = seedNode.Label
			}
			if isBoundary(path, label) {
				addEntrypoint(seedID)
			}
		}
	}

	return result
}
