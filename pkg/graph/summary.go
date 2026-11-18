package graph

import (
	"sort"
)

// GodNode represents a high-centrality functional symbol in the architecture.
type GodNode struct {
	Name        string `json:"name"`
	FilePath    string `json:"file_path"`
	InDegree    int    `json:"in_degree"`
	OutDegree   int    `json:"out_degree"`
	TotalDegree int    `json:"total_degree"`
}

// ArchitectureSummary provides a macro architectural overview of the indexed codebase.
type ArchitectureSummary struct {
	TotalFiles   int            `json:"total_files"`
	TotalSymbols int            `json:"total_symbols"`
	TotalEdges   int            `json:"total_edges"`
	EdgesByType  map[string]int `json:"edges_by_type"`
	GodNodes     []GodNode      `json:"god_nodes"`
}

// GetArchitectureSummary consolidates macro metrics and Top 10 God Nodes.
func (e *Engine) GetArchitectureSummary() (*ArchitectureSummary, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	totalFiles := 0
	totalSymbols := 0
	for _, node := range e.nodes {
		if node.Type == NodeFile {
			totalFiles++
		} else if node.Type == NodeSymbol {
			totalSymbols++
		}
	}

	totalEdges := len(e.edges)
	edgesByType := map[string]int{
		string(EdgeDefines): 0,
		string(EdgeCalls):   0,
		string(EdgeImports): 0,
	}
	for _, edge := range e.edges {
		edgesByType[string(edge.Type)]++
	}

	// Calculate God Nodes: only symbol nodes and only functional edges (calls, imports)
	godNodes := make([]GodNode, 0)
	for _, node := range e.nodes {
		if node.Type != NodeSymbol {
			continue
		}

		// Calculate functional InDegree
		inDegreeMap := make(map[string]bool)
		for _, p := range e.predecessors[node.ID] {
			if predNode, ok := e.nodes[p]; ok && predNode.Type != NodeFile {
				if e.isFunctionalEdge(p, node.ID) {
					inDegreeMap[p] = true
				}
			}
		}
		inDegree := len(inDegreeMap)

		// Calculate functional OutDegree
		outDegreeMap := make(map[string]bool)
		for _, s := range e.adjacency[node.ID] {
			if succNode, ok := e.nodes[s]; ok && succNode.Type != NodeFile {
				if e.isFunctionalEdge(node.ID, s) {
					outDegreeMap[s] = true
				}
			}
		}
		outDegree := len(outDegreeMap)

		totalDegree := inDegree + outDegree
		if totalDegree > 0 {
			godNodes = append(godNodes, GodNode{
				Name:        node.ID,
				FilePath:    node.FilePath,
				InDegree:    inDegree,
				OutDegree:   outDegree,
				TotalDegree: totalDegree,
			})
		}
	}

	// Sort descending by TotalDegree, break ties alphabetically by Name
	sort.Slice(godNodes, func(i, j int) bool {
		if godNodes[i].TotalDegree != godNodes[j].TotalDegree {
			return godNodes[i].TotalDegree > godNodes[j].TotalDegree
		}
		return godNodes[i].Name < godNodes[j].Name
	})

	if len(godNodes) > 10 {
		godNodes = godNodes[:10]
	}

	return &ArchitectureSummary{
		TotalFiles:   totalFiles,
		TotalSymbols: totalSymbols,
		TotalEdges:   totalEdges,
		EdgesByType:  edgesByType,
		GodNodes:     godNodes,
	}, nil
}
