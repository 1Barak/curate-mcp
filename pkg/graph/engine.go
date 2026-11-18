package graph

import (
	"sync"
)

// NodeType represents the categorical type of an AST or file node.
type NodeType string

const (
	NodeFile   NodeType = "file"
	NodeSymbol NodeType = "symbol"
)

// EdgeType represents the dependency relationship between two nodes.
type EdgeType string

const (
	EdgeCalls   EdgeType = "calls"
	EdgeImports EdgeType = "imports"
	EdgeDefines EdgeType = "defines"
)

// Node represents a structural element (file or symbol definition) in the graph.
type Node struct {
	ID       string   `json:"id"`
	Label    string   `json:"label"`
	FilePath string   `json:"file_path"`
	Type     NodeType `json:"type"`
	Imports  []string `json:"imports,omitempty"`
	Calls    []string `json:"calls,omitempty"`
}

// Edge represents a directed connection between two nodes.
type Edge struct {
	From string   `json:"from"`
	To   string   `json:"to"`
	Type EdgeType `json:"type"`
}

// Engine maintains the in-memory directed graph with concurrent read/write protection.
type Engine struct {
	mu           sync.RWMutex
	nodes        map[string]Node
	edges        map[string]Edge
	adjacency    map[string][]string // outgoing: from -> []to
	predecessors map[string][]string // incoming: to -> []from
}

// NewEngine creates an empty in-memory graph engine.
func NewEngine() *Engine {
	return &Engine{
		nodes:        make(map[string]Node),
		edges:        make(map[string]Edge),
		adjacency:    make(map[string][]string),
		predecessors: make(map[string][]string),
	}
}

// AddNode inserts or updates a node in the graph under lock.
func (e *Engine) AddNode(node Node) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.nodes[node.ID] = node
	return nil
}

// AddEdge inserts a directed edge from 'From' to 'To' under lock.
func (e *Engine) AddEdge(edge Edge) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	edgeKey := edge.From + "->" + edge.To + ":" + string(edge.Type)
	if _, exists := e.edges[edgeKey]; !exists {
		e.edges[edgeKey] = edge
		e.adjacency[edge.From] = append(e.adjacency[edge.From], edge.To)
		e.predecessors[edge.To] = append(e.predecessors[edge.To], edge.From)
	}
	return nil
}

// HasEdge checks if a directed edge of edgeType exists from 'from' to 'to'.
func (e *Engine) HasEdge(from, to string, edgeType EdgeType) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	edgeKey := from + "->" + to + ":" + string(edgeType)
	_, exists := e.edges[edgeKey]
	return exists
}

// GetNode retrieves a node by its identifier.
func (e *Engine) GetNode(id string) (Node, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	node, exists := e.nodes[id]
	return node, exists
}

// GetPredecessors returns all incoming source node IDs pointing to target id.
func (e *Engine) GetPredecessors(id string) []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	preds := e.predecessors[id]
	if len(preds) == 0 {
		return nil
	}
	res := make([]string, len(preds))
	copy(res, preds)
	return res
}

// GetSuccessors returns all outgoing destination node IDs pointed to by source id.
func (e *Engine) GetSuccessors(id string) []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	succs := e.adjacency[id]
	if len(succs) == 0 {
		return nil
	}
	res := make([]string, len(succs))
	copy(res, succs)
	return res
}

// NodeCount returns the total number of nodes stored.
func (e *Engine) NodeCount() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.nodes)
}

// EdgeCount returns the total number of edges stored.
func (e *Engine) EdgeCount() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.edges)
}

// GetAllNodes returns a snapshot of all nodes stored.
func (e *Engine) GetAllNodes() []Node {
	e.mu.RLock()
	defer e.mu.RUnlock()
	nodes := make([]Node, 0, len(e.nodes))
	for _, n := range e.nodes {
		nodes = append(nodes, n)
	}
	return nodes
}
