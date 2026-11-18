package mcp

import (
	"context"
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
}

// Server represents the curate-mcp server handler exposing MCP tools.
type Server struct {
	cfg    ServerConfig
	engine *graph.Engine
}

// NewServer initializes a new MCP Server with the given configuration.
func NewServer(cfg ServerConfig) *Server {
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = graph.HardCapTokens
	}
	return &Server{
		cfg:    cfg,
		engine: graph.NewEngine(),
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
