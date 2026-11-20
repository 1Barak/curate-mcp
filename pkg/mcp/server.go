package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/1Barak/curate-mcp/pkg/graph"
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
	Status                string                 `json:"status"`
	Project               string                 `json:"project"`
	Intent                string                 `json:"intent"`
	PrimaryFiles          []string               `json:"primary_files"`
	Entrypoints           []string               `json:"entrypoints"`
	DurationMS            int64                  `json:"duration_ms"`
	HasMore               bool                   `json:"has_more"`
	TotalMatchingFiles    int                    `json:"total_matching_files"`
	TotalMatchingSymbols  int                    `json:"total_matching_symbols"`
	TruncatedSymbolsCount int                    `json:"truncated_symbols_count"`
	NextOffsetFiles       int                    `json:"next_offset_files"`
	OffsetFiles           int                    `json:"offset_files"`
	StructuralDiagnostics *StructuralDiagnostics `json:"structural_diagnostics,omitempty"`
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
			if filePath != "" && !fileSet[filePath] && !strings.HasPrefix(item.node.ID, "$root:") {
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
			return &CurateSliceResponse{
				Status:                "ok",
				Project:               projectName,
				Intent:                req.Intent,
				PrimaryFiles:          primaryFiles,
				Entrypoints:           entrypoints,
				DurationMS:            time.Since(startTime).Milliseconds(),
				HasMore:               pruned.HasMore,
				TotalMatchingFiles:    pruned.TotalMatchingFiles,
				TotalMatchingSymbols:  pruned.TotalMatchingSymbols,
				TruncatedSymbolsCount: pruned.TruncatedSymbolsCount,
				NextOffsetFiles:       pruned.NextOffsetFiles,
				OffsetFiles:           pruned.OffsetFiles,
				StructuralDiagnostics: s.computeStructuralDiagnostics(primaryFiles),
			}, nil
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
		Status:                "ok",
		Project:               projectName,
		Intent:                req.Intent,
		PrimaryFiles:          primaryFiles,
		Entrypoints:           entrypoints,
		DurationMS:            duration,
		StructuralDiagnostics: s.computeStructuralDiagnostics(primaryFiles),
	}, nil
}

// RPCErrorDetails represents actionable suggestions and hints in JSON-RPC errors (RF-4).
type RPCErrorDetails struct {
	Suggestions []string `json:"suggestions"`
	Hint        string   `json:"hint"`
}

// RPCError represents a structured JSON-RPC protocol error (RF-4).
type RPCError struct {
	Code    int              `json:"code"`
	Message string           `json:"message"`
	Data    *RPCErrorDetails `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	return e.Message
}

var (
	ErrRPCInvalidParams             = &RPCError{Code: -32602, Message: "Invalid params"}
	ErrRPCReceiptsPathNotConfigured = &RPCError{Code: -32602, Message: "Invalid params: receipts path not configured"}
	ErrRPCNodeNotFound              = &RPCError{Code: -32602, Message: "Invalid params: node not found"}
)

// StructuralDiagnostics represents structural health diagnostics (RF-5 and SPEC-004 RF-3).
type StructuralDiagnostics struct {
	UnusedImports         []string `json:"unused_imports"`
	DeadSymbols           []string `json:"dead_symbols"`
	DeadPrivate           []string `json:"dead_private"`
	DeadExportedInternal  []string `json:"dead_exported_internal"`
	UnreferencedPublicAPI []string `json:"unreferenced_public_api"`
}

// BlastRadiusRequest defines the JSON-RPC input schema for tool get_blast_radius.
type BlastRadiusRequest struct {
	NodeName       string `json:"node_name"`
	Depth          int    `json:"depth,omitempty"`
	MaxNodes       int    `json:"max_nodes,omitempty"`
	PersistReceipt *bool  `json:"persist_receipt,omitempty"`
}

// BlastRadiusResponse defines the JSON-RPC output schema for tool get_blast_radius.
type BlastRadiusResponse struct {
	Status                string                 `json:"status"`
	NodeName              string                 `json:"node_name"`
	FilePath              string                 `json:"file_path"`
	InDegree              int                    `json:"in_degree"`
	OutDegree             int                    `json:"out_degree"`
	Criticality           int                    `json:"criticality"`
	Risk                  graph.RiskLevel        `json:"risk"`
	FTDEligible           bool                   `json:"ftd_eligible"`
	UpstreamCallers       []string               `json:"upstream_callers"`
	DownstreamCallees     []string               `json:"downstream_callees"`
	OTelTrace             string                 `json:"otel_trace"`
	ReceiptPersisted      bool                   `json:"receipt_persisted"`
	Truncated             bool                   `json:"truncated,omitempty"`
	StructuralDiagnostics *StructuralDiagnostics `json:"structural_diagnostics,omitempty"`
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
	Status       string          `json:"status"`
	TotalFiles   int             `json:"total_files"`
	TotalSymbols int             `json:"total_symbols"`
	TotalEdges   int             `json:"total_edges"`
	EdgesByType  map[string]int  `json:"edges_by_type"`
	GodNodes     []graph.GodNode `json:"god_nodes"`
	Truncated    bool            `json:"truncated,omitempty"`
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

// resolveNode resolves a symbol or file query with fuzzy suffix auto-resolution and actionable error hints (RF-4).
func (s *Server) resolveNode(query, toolName string) (graph.Node, *RPCError) {
	hint := fmt.Sprintf("Retry %s using one of the exact suggested symbol IDs above, or search first with curate_slice if suggestions is empty.", toolName)

	if s.engine == nil {
		return graph.Node{}, &RPCError{
			Code:    -32602,
			Message: fmt.Sprintf("Node '%s' not found.", query),
			Data: &RPCErrorDetails{
				Suggestions: []string{},
				Hint:        hint,
			},
		}
	}

	// 1. Exact match on Node.ID
	if node, exists := s.engine.GetNode(query); exists {
		return node, nil
	}

	allNodes := s.engine.GetAllNodes()

	// 2. Suffix search: node.ID ends in ":" + query or node.Label == query
	var suffixMatches []graph.Node
	for _, n := range allNodes {
		if strings.HasSuffix(n.ID, ":"+query) || n.Label == query {
			suffixMatches = append(suffixMatches, n)
		}
	}

	// Exactly 1 match: auto-resolve transparently
	if len(suffixMatches) == 1 {
		return suffixMatches[0], nil
	}

	// 3. Suffix / similarity scoring for suggestions
	type scoredCandidate struct {
		id    string
		score float64
	}

	candidateMap := make(map[string]float64)
	for _, sm := range suffixMatches {
		candidateMap[sm.ID] = 100.0
	}

	for _, n := range allNodes {
		if _, exists := candidateMap[n.ID]; exists {
			continue
		}
		sc := computeNodeSimilarity(query, n)
		if sc > 0 {
			candidateMap[n.ID] = sc
		}
	}

	candidates := make([]scoredCandidate, 0, len(candidateMap))
	for id, sc := range candidateMap {
		candidates = append(candidates, scoredCandidate{id: id, score: sc})
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		if len(candidates[i].id) != len(candidates[j].id) {
			return len(candidates[i].id) < len(candidates[j].id)
		}
		return candidates[i].id < candidates[j].id
	})

	suggestions := make([]string, 0, 5)
	for _, c := range candidates {
		suggestions = append(suggestions, c.id)
		if len(suggestions) == 5 {
			break
		}
	}

	var msg string
	if len(suffixMatches) > 1 {
		msg = fmt.Sprintf("Node '%s' is ambiguous (%d matches).", query, len(suffixMatches))
	} else {
		msg = fmt.Sprintf("Node '%s' not found.", query)
	}

	return graph.Node{}, &RPCError{
		Code:    -32602,
		Message: msg,
		Data: &RPCErrorDetails{
			Suggestions: suggestions,
			Hint:        hint,
		},
	}
}

// computeNodeSimilarity computes a heuristic similarity score between query and node.
func computeNodeSimilarity(query string, n graph.Node) float64 {
	qLower := strings.ToLower(query)
	labelLower := strings.ToLower(n.Label)
	idLower := strings.ToLower(n.ID)

	if strings.HasSuffix(n.ID, ":"+query) || n.Label == query {
		return 100.0
	}
	if strings.EqualFold(n.Label, query) {
		return 95.0
	}
	if strings.HasPrefix(labelLower, qLower) && len(labelLower) > 0 {
		return 80.0 + float64(len(qLower))/float64(len(labelLower))
	}
	if strings.HasSuffix(labelLower, qLower) && len(labelLower) > 0 {
		return 75.0 + float64(len(qLower))/float64(len(labelLower))
	}
	if strings.Contains(labelLower, qLower) && len(labelLower) > 0 {
		return 70.0 + float64(len(qLower))/float64(len(labelLower))
	}
	if len(qLower) >= 3 && isSubsequence(qLower, labelLower) {
		return 60.0 + float64(len(qLower))/float64(len(labelLower))
	}
	if strings.Contains(idLower, qLower) && len(idLower) > 0 {
		return 50.0 + float64(len(qLower))/float64(len(idLower))
	}
	if len(qLower) >= 4 {
		lcs := lcsLength(qLower, labelLower)
		ratio := float64(lcs) / float64(len(qLower))
		if ratio >= 0.7 {
			return 40.0 * ratio
		}
	}
	return 0.0
}

func isSubsequence(sub, str string) bool {
	i, j := 0, 0
	for i < len(sub) && j < len(str) {
		if sub[i] == str[j] {
			i++
		}
		j++
	}
	return i == len(sub)
}

func lcsLength(a, b string) int {
	m, n := len(a), len(b)
	if m == 0 || n == 0 {
		return 0
	}
	dp := make([]int, n+1)
	for i := 1; i <= m; i++ {
		prev := 0
		for j := 1; j <= n; j++ {
			tmp := dp[j]
			if a[i-1] == b[j-1] {
				dp[j] = prev + 1
			} else if dp[j-1] > dp[j] {
				dp[j] = dp[j-1]
			}
			prev = tmp
		}
	}
	return dp[n]
}

// isPrivateSymbol determines if a symbol is unexported/private (RF-5).
func isPrivateSymbol(sym graph.Node) bool {
	if len(sym.Label) == 0 {
		return false
	}
	ext := filepath.Ext(sym.FilePath)
	if ext == ".go" {
		return !unicode.IsUpper(rune(sym.Label[0]))
	}
	if ext == ".py" {
		return strings.HasPrefix(sym.Label, "_")
	}
	if strings.HasPrefix(sym.Label, "_") || strings.HasPrefix(sym.Label, "#") {
		return true
	}
	firstChar := sym.Label[:1]
	return strings.ToLower(firstChar) == firstChar && !unicode.IsUpper(rune(sym.Label[0]))
}

// isRuntimeEntrypoint determines if an unexported symbol is a runtime entrypoint (SPEC-004 RF-3).
func isRuntimeEntrypoint(sym graph.Node) bool {
	ext := filepath.Ext(sym.FilePath)
	label := sym.Label
	if ext == ".go" {
		if label == "main" || label == "init" {
			return true
		}
		if strings.HasSuffix(sym.FilePath, "_test.go") {
			if strings.HasPrefix(label, "Test") || strings.HasPrefix(label, "Benchmark") || strings.HasPrefix(label, "Example") {
				return true
			}
		}
	}
	if ext == ".py" {
		if label == "__init__" || label == "__main__" || label == "<module>" {
			return true
		}
	}
	return false
}

// isInternalPath determines if a file path is inside an internal directory (SPEC-004 RF-3).
func isInternalPath(p string) bool {
	clean := filepath.ToSlash(p)
	return strings.HasPrefix(clean, "internal/") || strings.Contains(clean, "/internal/")
}

// computeStructuralDiagnostics analyzes the given files for unused imports and dead private symbols (RF-5 and SPEC-004 RF-3).
func (s *Server) computeStructuralDiagnostics(targetFiles []string) *StructuralDiagnostics {
	if s.engine == nil || len(targetFiles) == 0 {
		return &StructuralDiagnostics{
			UnusedImports:         []string{},
			DeadSymbols:           []string{},
			DeadPrivate:           []string{},
			DeadExportedInternal:  []string{},
			UnreferencedPublicAPI: []string{},
		}
	}

	allNodes := s.engine.GetAllNodes()

	targetFileSet := make(map[string]bool)
	for _, f := range targetFiles {
		cleanF := filepath.ToSlash(f)
		targetFileSet[cleanF] = true
	}

	fileNodes := make([]graph.Node, 0)
	fileSymbols := make(map[string][]graph.Node)

	for _, n := range allNodes {
		cleanFilePath := filepath.ToSlash(n.FilePath)
		cleanID := filepath.ToSlash(n.ID)

		if n.Type == graph.NodeFile {
			if targetFileSet[cleanFilePath] || targetFileSet[cleanID] {
				fileNodes = append(fileNodes, n)
			}
		} else if n.Type == graph.NodeSymbol {
			for target := range targetFileSet {
				if cleanFilePath == target || cleanID == target || strings.HasPrefix(cleanID, target+":") {
					fileSymbols[target] = append(fileSymbols[target], n)
					break
				}
			}
		}
	}

	unusedImportsMap := make(map[string]bool)
	deadSymbolsMap := make(map[string]bool)
	deadPrivateMap := make(map[string]bool)
	deadExportedInternalMap := make(map[string]bool)
	unreferencedPublicAPIMap := make(map[string]bool)

	// 1. Detect unused imports
	for _, f := range fileNodes {
		cleanFPath := filepath.ToSlash(f.FilePath)
		cleanFID := filepath.ToSlash(f.ID)

		syms := fileSymbols[cleanFPath]
		if len(syms) == 0 {
			syms = fileSymbols[cleanFID]
		}

		for _, imp := range f.Imports {
			cleanImp := filepath.ToSlash(imp)
			impUsed := false

			for _, sym := range syms {
				// (a) Outgoing EdgeCalls to callee symbol
				succs := s.engine.GetSuccessors(sym.ID)
				for _, succID := range succs {
					if s.engine.HasEdge(sym.ID, succID, graph.EdgeCalls) {
						succNode, ok := s.engine.GetNode(succID)
						if ok {
							succFile := filepath.ToSlash(succNode.FilePath)
							succIDClean := filepath.ToSlash(succNode.ID)
							if succFile == cleanImp || succIDClean == cleanImp ||
								strings.HasPrefix(succFile, cleanImp) || strings.HasPrefix(succIDClean, cleanImp) ||
								strings.HasSuffix(cleanImp, succFile) {
								impUsed = true
								break
							}
						}
					}
				}
				if impUsed {
					break
				}

				// (b) Calls list directly
				for _, call := range sym.Calls {
					cleanCall := filepath.ToSlash(call)
					if cleanCall == cleanImp || strings.HasPrefix(cleanCall, cleanImp) || strings.Contains(cleanCall, cleanImp) {
						impUsed = true
						break
					}
				}
				if impUsed {
					break
				}
			}

			if !impUsed {
				unusedImportsMap[imp] = true
			}
		}
	}

	// 2. Classify dead symbols according to SPEC-004 RF-3 taxonomy
	for _, syms := range fileSymbols {
		for _, sym := range syms {
			inDegreeCalls := 0
			preds := s.engine.GetPredecessors(sym.ID)
			for _, p := range preds {
				if s.engine.HasEdge(p, sym.ID, graph.EdgeCalls) {
					inDegreeCalls++
				}
			}

			if inDegreeCalls == 0 {
				if isPrivateSymbol(sym) {
					if !isRuntimeEntrypoint(sym) {
						deadPrivateMap[sym.ID] = true
						deadSymbolsMap[sym.ID] = true
					}
				} else {
					if isInternalPath(sym.FilePath) {
						deadExportedInternalMap[sym.ID] = true
					} else {
						unreferencedPublicAPIMap[sym.ID] = true
					}
				}
			}
		}
	}

	const maxListSize = 25

	sortAndCap := func(m map[string]bool) []string {
		list := make([]string, 0, len(m))
		for k := range m {
			list = append(list, k)
		}
		sort.Strings(list)
		if len(list) > maxListSize {
			list = list[:maxListSize]
		}
		return list
	}

	return &StructuralDiagnostics{
		UnusedImports:         sortAndCap(unusedImportsMap),
		DeadSymbols:           sortAndCap(deadSymbolsMap),
		DeadPrivate:           sortAndCap(deadPrivateMap),
		DeadExportedInternal:  sortAndCap(deadExportedInternalMap),
		UnreferencedPublicAPI: sortAndCap(unreferencedPublicAPIMap),
	}
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

	resolvedNode, rpcErr := s.resolveNode(req.NodeName, "get_blast_radius")
	if rpcErr != nil {
		return nil, rpcErr
	}

	res, err := s.engine.CalculateBlastRadius(resolvedNode.ID, req.Depth, req.MaxNodes)
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

	filesToDiag := make([]string, 0, 1)
	if res.FilePath != "" {
		filesToDiag = append(filesToDiag, res.FilePath)
	} else if resolvedNode.FilePath != "" {
		filesToDiag = append(filesToDiag, resolvedNode.FilePath)
	} else if resolvedNode.Type == graph.NodeFile {
		filesToDiag = append(filesToDiag, resolvedNode.ID)
	}
	resp.StructuralDiagnostics = s.computeStructuralDiagnostics(filesToDiag)

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

	nodeA, rpcErr := s.resolveNode(req.NodeA, "find_path")
	if rpcErr != nil {
		return nil, rpcErr
	}

	nodeB, rpcErr := s.resolveNode(req.NodeB, "find_path")
	if rpcErr != nil {
		return nil, rpcErr
	}

	res, err := s.engine.FindShortestPath(nodeA.ID, nodeB.ID)
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
