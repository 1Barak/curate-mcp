package graph

// PathResult holds the shortest directed path search outcome between two nodes.
type PathResult struct {
	Source    string   `json:"source"`
	Target    string   `json:"target"`
	Connected bool     `json:"connected"`
	Path      []string `json:"path"`
	Length    int      `json:"length"`
}

// FindShortestPath performs a BFS search on functional edges (calls, imports)
// between source and target, excluding defines edges.
func (e *Engine) FindShortestPath(source, target string) (*PathResult, error) {
	if source == "" || target == "" {
		return nil, ErrInvalidParams
	}

	e.mu.RLock()
	defer e.mu.RUnlock()

	if _, exists := e.nodes[source]; !exists {
		return nil, ErrNodeNotFound
	}
	if _, exists := e.nodes[target]; !exists {
		return nil, ErrNodeNotFound
	}

	// Trivial case: source == target
	if source == target {
		return &PathResult{
			Source:    source,
			Target:    target,
			Connected: true,
			Path:      []string{source},
			Length:    0,
		}, nil
	}

	visited := make(map[string]bool)
	visited[source] = true
	parent := make(map[string]string)
	queue := []string{source}
	found := false

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]

		if curr == target {
			found = true
			break
		}

		for _, s := range e.adjacency[curr] {
			if node, ok := e.nodes[s]; ok && node.Type != NodeFile {
				if e.isFunctionalEdge(curr, s) && !visited[s] {
					visited[s] = true
					parent[s] = curr
					if s == target {
						found = true
						break
					}
					queue = append(queue, s)
				}
			}
		}
		if found {
			break
		}
	}

	if !found {
		return &PathResult{
			Source:    source,
			Target:    target,
			Connected: false,
			Path:      []string{},
			Length:    0,
		}, nil
	}

	// Reconstruct path
	curr := target
	var revPath []string
	for {
		revPath = append(revPath, curr)
		if curr == source {
			break
		}
		curr = parent[curr]
	}

	path := make([]string, len(revPath))
	for i, n := range revPath {
		path[len(revPath)-1-i] = n
	}

	return &PathResult{
		Source:    source,
		Target:    target,
		Connected: true,
		Path:      path,
		Length:    len(path) - 1,
	}, nil
}
