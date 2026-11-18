package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrUnsupportedFormat is returned when an unsupported architecture summary format is requested (RF-7).
var ErrUnsupportedFormat = &RPCError{
	Code:    -32602,
	Message: "Invalid params: unsupported format (supported: 'json', 'markdown')",
}

// ArchitectureSummaryRequest defines the input parameters for get_architecture_summary (RF-7).
type ArchitectureSummaryRequest struct {
	Format string `json:"format,omitempty"`
}

// ArchitectureSummaryFormattedResponse defines the dual-format output schema for get_architecture_summary (RF-7).
type ArchitectureSummaryFormattedResponse struct {
	Format   string                       `json:"format"`
	JSON     *ArchitectureSummaryResponse `json:"json,omitempty"`
	Markdown string                       `json:"markdown,omitempty"`
	Raw      string                       `json:"raw,omitempty"`
}

type ultralightArchitectureJSON struct {
	Status       string         `json:"status"`
	TotalFiles   int            `json:"total_files"`
	TotalSymbols int            `json:"total_symbols"`
	TotalEdges   int            `json:"total_edges"`
	EdgesByType  map[string]int `json:"edges_by_type"`
	GodNodes     []string       `json:"god_nodes"`
}

// FormatArchitectureSummary formats an ArchitectureSummaryResponse according to the requested format (RF-7).
// Supported formats: "json" (or ""), "markdown".
func FormatArchitectureSummary(res *ArchitectureSummaryResponse, format string) (string, error) {
	if res == nil {
		return "", errors.New("mcp: nil architecture summary response")
	}

	normFormat := strings.ToLower(strings.TrimSpace(format))
	switch normFormat {
	case "", "json":
		return formatJSONUltralight(res)
	case "markdown":
		return formatMarkdownSummary(res)
	default:
		return "", ErrUnsupportedFormat
	}
}

func formatJSONUltralight(res *ArchitectureSummaryResponse) (string, error) {
	edgesByType := res.EdgesByType
	if edgesByType == nil {
		edgesByType = map[string]int{}
	}

	status := res.Status
	if status == "" {
		status = "ok"
	}

	godNodes := make([]string, 0)
	for _, gn := range res.GodNodes {
		candidate := append(godNodes, gn.Name)
		testObj := ultralightArchitectureJSON{
			Status:       status,
			TotalFiles:   res.TotalFiles,
			TotalSymbols: res.TotalSymbols,
			TotalEdges:   res.TotalEdges,
			EdgesByType:  edgesByType,
			GodNodes:     candidate,
		}
		b, err := json.Marshal(testObj)
		if err == nil && len(b) <= 250 {
			godNodes = candidate
		} else {
			break
		}
	}

	finalObj := ultralightArchitectureJSON{
		Status:       status,
		TotalFiles:   res.TotalFiles,
		TotalSymbols: res.TotalSymbols,
		TotalEdges:   res.TotalEdges,
		EdgesByType:  edgesByType,
		GodNodes:     godNodes,
	}

	b, err := json.Marshal(finalObj)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func formatMarkdownSummary(res *ArchitectureSummaryResponse) (string, error) {
	var sb strings.Builder

	calls := 0
	defines := 0
	imports := 0
	if res.EdgesByType != nil {
		calls = res.EdgesByType["calls"]
		defines = res.EdgesByType["defines"]
		imports = res.EdgesByType["imports"]
	}

	sb.WriteString("# Architecture Summary\n\n")
	sb.WriteString("## Health Metrics\n")
	fmt.Fprintf(&sb, "- **Files**: %d\n", res.TotalFiles)
	fmt.Fprintf(&sb, "- **Symbols**: %d\n", res.TotalSymbols)
	fmt.Fprintf(&sb, "- **Edges**: %d (calls: %d, defines: %d, imports: %d)\n\n", res.TotalEdges, calls, defines, imports)

	sb.WriteString("## God Nodes (Top 5)\n")
	topGodNodes := res.GodNodes
	if len(topGodNodes) > 5 {
		topGodNodes = topGodNodes[:5]
	}

	if len(topGodNodes) == 0 {
		sb.WriteString("No God Nodes detected in the topology.\n\n")
	} else {
		for _, gn := range topGodNodes {
			fmt.Fprintf(&sb, "- `%s`: total_degree=%d (in=%d, out=%d)\n", gn.Name, gn.TotalDegree, gn.InDegree, gn.OutDegree)
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## Structural Warnings\n")
	warningsCount := 0
	if imports == 0 && res.TotalEdges > 0 {
		sb.WriteString("- Warning: No cross-module import edges detected.\n")
		warningsCount++
	}
	if calls == 0 && res.TotalEdges > 0 {
		sb.WriteString("- Warning: No functional call edges detected.\n")
		warningsCount++
	}
	if warningsCount == 0 {
		sb.WriteString("- Status: Topology health is nominal with zero critical warnings.\n")
	}

	md := sb.String()
	if len(md) > 4096 {
		idx := strings.LastIndex(md[:4096], "\n")
		if idx > 0 {
			md = md[:idx] + "\n"
		} else {
			md = md[:4096]
		}
	}

	return md, nil
}

// GetArchitectureSummaryFormatted executes get_architecture_summary with dual format support (RF-7).
func (s *Server) GetArchitectureSummaryFormatted(ctx context.Context, req ArchitectureSummaryRequest) (*ArchitectureSummaryFormattedResponse, error) {
	normFormat := strings.ToLower(strings.TrimSpace(req.Format))
	if normFormat != "" && normFormat != "json" && normFormat != "markdown" {
		return nil, ErrUnsupportedFormat
	}

	res, err := s.GetArchitectureSummary(ctx)
	if err != nil {
		return nil, err
	}

	raw, err := FormatArchitectureSummary(res, normFormat)
	if err != nil {
		return nil, err
	}

	resp := &ArchitectureSummaryFormattedResponse{
		Format: normFormat,
		Raw:    raw,
	}

	if normFormat == "" || normFormat == "json" {
		resp.Format = "json"
		resp.JSON = res
	} else if normFormat == "markdown" {
		resp.Markdown = raw
	}

	return resp, nil
}
