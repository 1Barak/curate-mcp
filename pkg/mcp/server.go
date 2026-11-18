package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"curate-mcp/pkg/graph"
)

// Security errors for path validation.
var (
	ErrPathTraversal        = errors.New("security: path traversal escape attempt detected")
	ErrInvalidPath          = errors.New("security: path is invalid or outside workspace boundary")
	ErrServerNotImplemented = errors.New("mcp: curate_slice execution not implemented")
)

// CurateSliceRequest defines the JSON-RPC input schema for tool `curate_slice`.
type CurateSliceRequest struct {
	Intent    string `json:"intent"`
	MaxFiles  int    `json:"max_files"`
	Workspace string `json:"workspace,omitempty"`
}

// CurateSliceResponse defines the JSON-RPC output schema for tool `curate_slice`.
type CurateSliceResponse struct {
	Status       string   `json:"status"`
	Project      string   `json:"project"`
	Intent       string   `json:"intent"`
	PrimaryFiles []string `json:"primary_files"`
	Entrypoints  []string `json:"entrypoints"`
	DurationMS   int64    `json:"duration_ms"`
}

// ServerConfig configures the MCP server instance.
type ServerConfig struct {
	WorkspaceRoot string
	MaxTokens     int
	ReceiptsPath  string
}

// Server represents the curate-mcp server handler exposing MCP tools.
type Server struct {
	cfg     ServerConfig
	engine  *graph.Engine
	emitter *ReceiptEmitter
}

// NewServer initializes a new MCP Server with the given configuration.
func NewServer(cfg ServerConfig) *Server {
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = graph.HardCapTokens
	}
	var emitter *ReceiptEmitter
	if cfg.ReceiptsPath != "" {
		emitter = NewReceiptEmitter(cfg.ReceiptsPath)
	}
	return &Server{
		cfg:     cfg,
		engine:  graph.NewEngine(),
		emitter: emitter,
	}
}

// SetEngine attaches an existing in-memory graph engine to the server.
func (s *Server) SetEngine(engine *graph.Engine) {
	s.engine = engine
}

// GetEngine returns the underlying graph engine.
func (s *Server) GetEngine() *graph.Engine {
	return s.engine
}

// GetEmitter returns the attached receipt emitter.
func (s *Server) GetEmitter() *ReceiptEmitter {
	return s.emitter
}

// ValidateWorkspacePath enforces defensive security against path traversal attacks.
// Rejects path escapes outside WorkspaceRoot, null bytes, and traversal patterns.
func (s *Server) ValidateWorkspacePath(requestedPath string) (string, error) {
	if strings.ContainsRune(requestedPath, 0) {
		return "", ErrPathTraversal
	}

	rawRoot := s.cfg.WorkspaceRoot
	if rawRoot == "" {
		rawRoot = "."
	}

	normRoot := filepath.ToSlash(rawRoot)
	normRoot = path.Clean(normRoot)

	if requestedPath == "" {
		return normRoot, nil
	}

	normReq := filepath.ToSlash(requestedPath)

	// Check if requestedPath is an absolute path
	isAbs := false
	if strings.HasPrefix(normReq, "/") {
		isAbs = true
	} else if len(normReq) >= 2 && normReq[1] == ':' {
		isAbs = true
	} else if filepath.IsAbs(requestedPath) {
		isAbs = true
	}

	if isAbs {
		cleanAbs := path.Clean(normReq)
		// Must be exactly normRoot or inside normRoot
		if cleanAbs == normRoot {
			return cleanAbs, nil
		}
		prefix := normRoot
		if !strings.HasSuffix(prefix, "/") {
			prefix += "/"
		}
		if strings.HasPrefix(cleanAbs, prefix) {
			return cleanAbs, nil
		}
		return "", ErrPathTraversal
	}

	// Relative path check
	cleanRel := path.Clean(normReq)
	if cleanRel == ".." || strings.HasPrefix(cleanRel, "../") || strings.HasPrefix(cleanRel, "..") {
		return "", ErrPathTraversal
	}

	joined := path.Join(normRoot, cleanRel)
	cleanJoined := path.Clean(joined)

	prefix := normRoot
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	if cleanJoined != normRoot && !strings.HasPrefix(cleanJoined, prefix) {
		return "", ErrPathTraversal
	}

	return cleanJoined, nil
}

// CurateSlice executes the core MCP tool logic:
// Intent query -> Graph scoring -> Entrypoints -> Cascading pruning -> Response.
func (s *Server) CurateSlice(ctx context.Context, req CurateSliceRequest) (*CurateSliceResponse, error) {
	startTime := time.Now()

	targetWorkspace := req.Workspace
	if targetWorkspace == "" {
		targetWorkspace = s.cfg.WorkspaceRoot
	}

	validatedWS, err := s.ValidateWorkspacePath(targetWorkspace)
	if err != nil {
		return nil, err
	}

	projectName := filepath.Base(validatedWS)
	if projectName == "." || projectName == "/" || projectName == "" || projectName == "\\" {
		projectName = "curate-mcp"
	}

	maxFiles := req.MaxFiles
	if maxFiles <= 0 {
		maxFiles = 10
	}

	primaryFiles := make([]string, 0)
	entrypoints := make([]string, 0)

	if s.engine != nil && s.engine.NodeCount() > 0 {
		tokens := graph.NormalizeIntent(req.Intent)
		nodes := s.engine.GetAllNodes()

		type scoredNode struct {
			node  graph.Node
			score float64
		}

		var scored []scoredNode
		for _, n := range nodes {
			sc := graph.CalculateScore(tokens, n)
			if sc > 0 {
				scored = append(scored, scoredNode{node: n, score: sc})
			}
		}

		// Sort descending by score
		sort.Slice(scored, func(i, j int) bool {
			return scored[i].score > scored[j].score
		})

		fileSet := make(map[string]bool)
		var seedIDs []string

		for _, item := range scored {
			filePath := item.node.FilePath
			if filePath != "" && !fileSet[filePath] {
				fileSet[filePath] = true
				primaryFiles = append(primaryFiles, filePath)
				seedIDs = append(seedIDs, item.node.ID)
				if len(primaryFiles) >= maxFiles {
					break
				}
			}
		}

		if len(seedIDs) > 0 {
			entrypoints = s.engine.FindEntrypoints(seedIDs)
		}

		// Build unpruned slice
		curated := &graph.CuratedSlice{
			Status:       "ok",
			Project:      projectName,
			Intent:       req.Intent,
			PrimaryFiles: primaryFiles,
			Entrypoints:  entrypoints,
			Symbols:      nodes,
		}

		pruned, pruneErr := graph.PruneSlice(curated, s.cfg.MaxTokens)
		if pruneErr == nil && pruned != nil {
			primaryFiles = pruned.PrimaryFiles
			entrypoints = pruned.Entrypoints
		}
	}

	if primaryFiles == nil {
		primaryFiles = []string{}
	}
	if entrypoints == nil {
		entrypoints = []string{}
	}

	duration := time.Since(startTime).Milliseconds()

	return &CurateSliceResponse{
		Status:       "ok",
		Project:      projectName,
		Intent:       req.Intent,
		PrimaryFiles: primaryFiles,
		Entrypoints:  entrypoints,
		DurationMS:   duration,
	}, nil
}

// RPCError represents a structured JSON-RPC protocol error.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string {
	return e.Message
}

var (
	ErrRPCInvalidParams            = &RPCError{Code: -32602, Message: "Invalid params"}
	ErrRPCReceiptsPathNotConfigured = &RPCError{Code: -32602, Message: "Invalid params: receipts path not configured"}
	ErrRPCNodeNotFound             = &RPCError{Code: -32602, Message: "Invalid params: node not found"}
)

// BlastRadiusRequest defines the JSON-RPC input schema for tool get_blast_radius.
type BlastRadiusRequest struct {
	NodeName       string `json:"node_name"`
	Depth          int    `json:"depth,omitempty"`
	MaxNodes       int    `json:"max_nodes,omitempty"`
	PersistReceipt *bool  `json:"persist_receipt,omitempty"`
}

// BlastRadiusResponse defines the JSON-RPC output schema for tool get_blast_radius.
type BlastRadiusResponse struct {
	Status            string          `json:"status"`
	NodeName          string          `json:"node_name"`
	FilePath          string          `json:"file_path"`
	InDegree          int             `json:"in_degree"`
	OutDegree         int             `json:"out_degree"`
	Criticality       int             `json:"criticality"`
	Risk              graph.RiskLevel `json:"risk"`
	FTDEligible       bool            `json:"ftd_eligible"`
	UpstreamCallers   []string        `json:"upstream_callers"`
	DownstreamCallees []string        `json:"downstream_callees"`
	OTelTrace         string          `json:"otel_trace"`
	ReceiptPersisted  bool            `json:"receipt_persisted"`
	Truncated         bool            `json:"truncated,omitempty"`
}

// FindPathRequest defines the JSON-RPC input schema for tool find_path.
type FindPathRequest struct {
	NodeA string `json:"node_a"`
	NodeB string `json:"node_b"`
}

// FindPathResponse defines the JSON-RPC output schema for tool find_path.
type FindPathResponse struct {
	Status    string   `json:"status"`
	Source    string   `json:"source"`
	Target    string   `json:"target"`
	Connected bool     `json:"connected"`
	Path      []string `json:"path"`
	Length    int      `json:"length"`
	Truncated bool     `json:"truncated,omitempty"`
}

// ArchitectureSummaryResponse defines the JSON-RPC output schema for tool get_architecture_summary.
type ArchitectureSummaryResponse struct {
	Status       string                 `json:"status"`
	TotalFiles   int                    `json:"total_files"`
	TotalSymbols int                    `json:"total_symbols"`
	TotalEdges   int                    `json:"total_edges"`
	EdgesByType  map[string]int         `json:"edges_by_type"`
	GodNodes     []graph.GodNode        `json:"god_nodes"`
	Truncated    bool                   `json:"truncated,omitempty"`
}

// ResolveReceiptsPath resolves the effective receipts file path respecting CLI flag > ENV var precedence.
func ResolveReceiptsPath(cliFlag string, envVar string) string {
	if cliFlag != "" {
		return cliFlag
	}
	if envVar != "" {
		return envVar
	}
	return ""
}

// GetBlastRadius executes the get_blast_radius tool.
func (s *Server) GetBlastRadius(ctx context.Context, req BlastRadiusRequest) (*BlastRadiusResponse, error) {
	if req.NodeName == "" {
		return nil, ErrRPCInvalidParams
	}

	hasPath := s.emitter != nil && s.emitter.GetReceiptsPath() != ""
	if !hasPath && req.PersistReceipt != nil && *req.PersistReceipt {
		return nil, ErrRPCReceiptsPathNotConfigured
	}

	res, err := s.engine.CalculateBlastRadius(req.NodeName, req.Depth, req.MaxNodes)
	if err != nil {
		if errors.Is(err, graph.ErrInvalidParams) {
			return nil, ErrRPCInvalidParams
		}
		if errors.Is(err, graph.ErrNodeNotFound) {
			return nil, ErrRPCNodeNotFound
		}
		return nil, err
	}

	persist := false
	if hasPath {
		if req.PersistReceipt == nil || *req.PersistReceipt {
			persist = true
		}
	}

	if persist {
		entry := ReceiptEntry{
			TimestampEpoch:  float64(time.Now().Unix()),
			TTLSeconds:      900,
			CodeSymbol:      res.NodeName,
			CodeFilePath:    res.FilePath,
			TraceID:         res.TraceID,
			InDegree:        res.InDegree,
			OutDegree:       res.OutDegree,
			IC:              res.Criticality,
			FTDEligible:     res.FTDEligible,
			UpstreamCallers: res.UpstreamCallers,
			AutoEmitted:     true,
		}
		if err := s.emitter.EmitReceipt(entry); err != nil {
			return nil, err
		}
	}

	resp := &BlastRadiusResponse{
		Status:            "ok",
		NodeName:          res.NodeName,
		FilePath:          res.FilePath,
		InDegree:          res.InDegree,
		OutDegree:         res.OutDegree,
		Criticality:       res.Criticality,
		Risk:              res.Risk,
		FTDEligible:       res.FTDEligible,
		UpstreamCallers:   res.UpstreamCallers,
		DownstreamCallees: res.DownstreamCallees,
		OTelTrace:         res.OTelTrace,
		ReceiptPersisted:  persist,
	}

	const maxBytesLimit = 3500
	rawBytes, err := json.Marshal(resp)
	if err == nil && len(rawBytes) > maxBytesLimit {
		resp.Truncated = true
		for len(rawBytes) > maxBytesLimit && (len(resp.UpstreamCallers) > 0 || len(resp.DownstreamCallees) > 0) {
			if len(resp.UpstreamCallers) >= len(resp.DownstreamCallees) && len(resp.UpstreamCallers) > 0 {
				cut := len(resp.UpstreamCallers) / 2
				if cut == len(resp.UpstreamCallers) {
					cut--
				}
				resp.UpstreamCallers = resp.UpstreamCallers[:cut]
			} else if len(resp.DownstreamCallees) > 0 {
				cut := len(resp.DownstreamCallees) / 2
				if cut == len(resp.DownstreamCallees) {
					cut--
				}
				resp.DownstreamCallees = resp.DownstreamCallees[:cut]
			}
			rawBytes, _ = json.Marshal(resp)
		}
	}

	return resp, nil
}

// FindPath executes the find_path tool.
func (s *Server) FindPath(ctx context.Context, req FindPathRequest) (*FindPathResponse, error) {
	if req.NodeA == "" || req.NodeB == "" {
		return nil, ErrRPCInvalidParams
	}

	res, err := s.engine.FindShortestPath(req.NodeA, req.NodeB)
	if err != nil {
		if errors.Is(err, graph.ErrInvalidParams) {
			return nil, ErrRPCInvalidParams
		}
		if errors.Is(err, graph.ErrNodeNotFound) {
			return nil, ErrRPCNodeNotFound
		}
		return nil, err
	}

	resp := &FindPathResponse{
		Status:    "ok",
		Source:    res.Source,
		Target:    res.Target,
		Connected: res.Connected,
		Path:      res.Path,
		Length:    res.Length,
	}

	const maxBytesLimit = 3500
	rawBytes, err := json.Marshal(resp)
	if err == nil && len(rawBytes) > maxBytesLimit {
		resp.Truncated = true
		for len(rawBytes) > maxBytesLimit && len(resp.Path) > 2 {
			resp.Path = append(resp.Path[:len(resp.Path)-2], resp.Path[len(resp.Path)-1])
			rawBytes, _ = json.Marshal(resp)
		}
	}

	return resp, nil
}

// GetArchitectureSummary executes the get_architecture_summary tool.
func (s *Server) GetArchitectureSummary(ctx context.Context) (*ArchitectureSummaryResponse, error) {
	res, err := s.engine.GetArchitectureSummary()
	if err != nil {
		return nil, err
	}

	resp := &ArchitectureSummaryResponse{
		Status:       "ok",
		TotalFiles:   res.TotalFiles,
		TotalSymbols: res.TotalSymbols,
		TotalEdges:   res.TotalEdges,
		EdgesByType:  res.EdgesByType,
		GodNodes:     res.GodNodes,
	}

	const maxBytesLimit = 3500
	rawBytes, err := json.Marshal(resp)
	if err == nil && len(rawBytes) > maxBytesLimit {
		resp.Truncated = true
		for len(rawBytes) > maxBytesLimit && len(resp.GodNodes) > 0 {
			resp.GodNodes = resp.GodNodes[:len(resp.GodNodes)-1]
			rawBytes, _ = json.Marshal(resp)
		}
	}

	return resp, nil
}
