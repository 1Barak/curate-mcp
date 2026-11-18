package graph

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"sort"
)

// RiskLevel represents the categorical risk classification for blast radius.
type RiskLevel string

const (
	RiskBajo    RiskLevel = "BAJO"
	RiskMedio   RiskLevel = "MEDIO"
	RiskAlto    RiskLevel = "ALTO"
	RiskCritico RiskLevel = "CRÍTICO"
)

// Standard graph errors
var (
	ErrNodeNotFound   = errors.New("graph: node not found")
	ErrInvalidParams  = errors.New("graph: invalid parameters")
	ErrNotImplemented = errors.New("graph: not implemented")
)

// BlastRadiusResult holds the calculation results for a node's blast radius and criticality.
type BlastRadiusResult struct {
	NodeName          string    `json:"node_name"`
	FilePath          string    `json:"file_path"`
	InDegree          int       `json:"in_degree"`
	OutDegree         int       `json:"out_degree"`
	Criticality       int       `json:"criticality"`
	Risk              RiskLevel `json:"risk"`
	FTDEligible       bool      `json:"ftd_eligible"`
	UpstreamCallers   []string  `json:"upstream_callers"`
	DownstreamCallees []string  `json:"downstream_callees"`
	TraceID           string    `json:"trace_id"`
	OTelTrace         string    `json:"otel_trace"`
}

// isFunctionalEdge checks if a directed edge from 'from' to 'to' is a calls or imports edge.
func (e *Engine) isFunctionalEdge(from, to string) bool {
	if _, ok := e.edges[from+"->"+to+":"+string(EdgeCalls)]; ok {
		return true
	}
	if _, ok := e.edges[from+"->"+to+":"+string(EdgeImports)]; ok {
		return true
	}
	return false
}

// GenerateUUIDv4 generates an RFC 4122 compliant UUIDv4 using crypto/rand.
func GenerateUUIDv4() (string, error) {
	var b [16]byte
	if _, err := io.ReadFull(rand.Reader, b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40 // Version 4
	b[8] = (b[8] & 0x3f) | 0x80 // Variant RFC 4122
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// CalculateBlastRadius calculates upstream callers P(u), downstream callees S(u),
// direct functional in/out degree, criticality Ic, risk level, and OTel trace.
func (e *Engine) CalculateBlastRadius(nodeName string, depth int, maxNodes int) (*BlastRadiusResult, error) {
	if nodeName == "" {
		return nil, ErrInvalidParams
	}

	if depth <= 0 {
		depth = 2
	} else if depth > 5 {
		depth = 5
	}

	if maxNodes <= 0 {
		maxNodes = 50
	} else if maxNodes > 200 {
		maxNodes = 200
	}

	e.mu.RLock()
	defer e.mu.RUnlock()

	targetNode, exists := e.nodes[nodeName]
	if !exists {
		return nil, ErrNodeNotFound
	}

	// Calculate direct 1-hop functional degrees (in_degree and out_degree)
	// incident on nodeName, strictly ignoring EdgeDefines and file nodes.
	directCallers := make(map[string]bool)
	for _, p := range e.predecessors[nodeName] {
		if node, ok := e.nodes[p]; ok && node.Type != NodeFile {
			if e.isFunctionalEdge(p, nodeName) {
				directCallers[p] = true
			}
		}
	}
	inDegree := len(directCallers)

	directCallees := make(map[string]bool)
	for _, s := range e.adjacency[nodeName] {
		if node, ok := e.nodes[s]; ok && node.Type != NodeFile {
			if e.isFunctionalEdge(nodeName, s) {
				directCallees[s] = true
			}
		}
	}
	outDegree := len(directCallees)

	// Criticality formula: Ic = (in_degree * 3) + out_degree
	ic := (inDegree * 3) + outDegree

	var risk RiskLevel
	var ftdEligible bool
	if ic < 3 {
		risk = RiskBajo
		ftdEligible = true
	} else if ic < 6 {
		risk = RiskMedio
		ftdEligible = false
	} else if ic < 12 {
		risk = RiskAlto
		ftdEligible = false
	} else {
		risk = RiskCritico
		ftdEligible = false
	}

	// BFS for UpstreamCallers P(u)
	upstreamCallers := make([]string, 0)
	visitedUpstream := make(map[string]bool)
	visitedUpstream[nodeName] = true

	type queueItem struct {
		id    string
		depth int
	}
	qUp := []queueItem{{id: nodeName, depth: 0}}

	for len(qUp) > 0 && len(upstreamCallers) < maxNodes {
		curr := qUp[0]
		qUp = qUp[1:]
		if curr.depth >= depth {
			continue
		}

		var preds []string
		predSet := make(map[string]bool)
		for _, p := range e.predecessors[curr.id] {
			if node, ok := e.nodes[p]; ok && node.Type != NodeFile && !visitedUpstream[p] && !predSet[p] {
				if e.isFunctionalEdge(p, curr.id) {
					predSet[p] = true
					preds = append(preds, p)
				}
			}
		}
		sort.Strings(preds)
		for _, p := range preds {
			if !visitedUpstream[p] {
				visitedUpstream[p] = true
				upstreamCallers = append(upstreamCallers, p)
				if len(upstreamCallers) >= maxNodes {
					break
				}
				qUp = append(qUp, queueItem{id: p, depth: curr.depth + 1})
			}
		}
	}

	// BFS for DownstreamCallees S(u)
	downstreamCallees := make([]string, 0)
	visitedDownstream := make(map[string]bool)
	visitedDownstream[nodeName] = true

	qDown := []queueItem{{id: nodeName, depth: 0}}

	for len(qDown) > 0 && len(downstreamCallees) < maxNodes {
		curr := qDown[0]
		qDown = qDown[1:]
		if curr.depth >= depth {
			continue
		}

		var succs []string
		succSet := make(map[string]bool)
		for _, s := range e.adjacency[curr.id] {
			if node, ok := e.nodes[s]; ok && node.Type != NodeFile && !visitedDownstream[s] && !succSet[s] {
				if e.isFunctionalEdge(curr.id, s) {
					succSet[s] = true
					succs = append(succs, s)
				}
			}
		}
		sort.Strings(succs)
		for _, s := range succs {
			if !visitedDownstream[s] {
				visitedDownstream[s] = true
				downstreamCallees = append(downstreamCallees, s)
				if len(downstreamCallees) >= maxNodes {
					break
				}
				qDown = append(qDown, queueItem{id: s, depth: curr.depth + 1})
			}
		}
	}

	traceID, err := GenerateUUIDv4()
	if err != nil {
		return nil, err
	}

	otelTrace := fmt.Sprintf("[FTD_METRICS: trace_id=%s, in_degree=%d, out_degree=%d, ic=%d, ftd_eligible=%t]",
		traceID, inDegree, outDegree, ic, ftdEligible)

	return &BlastRadiusResult{
		NodeName:          nodeName,
		FilePath:          targetNode.FilePath,
		InDegree:          inDegree,
		OutDegree:         outDegree,
		Criticality:       ic,
		Risk:              risk,
		FTDEligible:       ftdEligible,
		UpstreamCallers:   upstreamCallers,
		DownstreamCallees: downstreamCallees,
		TraceID:           traceID,
		OTelTrace:         otelTrace,
	}, nil
}
