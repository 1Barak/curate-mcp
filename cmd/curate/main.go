package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sync"
	"syscall"
	"time"

	"github.com/1Barak/curate-mcp/pkg/graph"
	"github.com/1Barak/curate-mcp/pkg/mcp"
	"github.com/1Barak/curate-mcp/pkg/parser"
	"github.com/1Barak/curate-mcp/pkg/watcher"
)

var jsonBufferPool = sync.Pool{
	New: func() interface{} {
		return bytes.NewBuffer(make([]byte, 0, 4096))
	},
}

func marshalIndentWithPool(v interface{}) ([]byte, error) {
	buf := jsonBufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer jsonBufferPool.Put(buf)

	enc := json.NewEncoder(buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	b := bytes.TrimRight(buf.Bytes(), "\n")
	res := make([]byte, len(b))
	copy(res, b)
	return res, nil
}

const (
	Version                    = "1.3.0"
	ToolName                   = "curate_slice"
	ToolCurateSlice            = "curate_slice"
	ToolGetBlastRadius         = "get_blast_radius"
	ToolFindPath               = "find_path"
	ToolGetArchitectureSummary = "get_architecture_summary"
)

// JSON-RPC basic structures for MCP stdio protocol
type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonrpcResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *rpcError   `json:"error,omitempty"`
}

type rpcError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

func main() {
	// RF-6: Configure RAM cap to 200 MiB
	debug.SetMemoryLimit(200 << 20)

	var (
		workspaceFlag    string
		maxTokensFlag    int
		receiptsPathFlag string
		versionFlag      bool
	)

	flag.StringVar(&workspaceFlag, "workspace", "", "Path to codebase workspace root")
	flag.StringVar(&workspaceFlag, "w", "", "Path to codebase workspace root (shorthand)")
	flag.IntVar(&maxTokensFlag, "max-tokens", graph.HardCapTokens, "Hard token budget limit for curated slices")
	flag.StringVar(&receiptsPathFlag, "receipts-path", "", "Path to OTel receipts JSON file (e.g. runtime/state/blast_radius_receipts.json)")
	flag.BoolVar(&versionFlag, "version", false, "Print curate-mcp version")
	flag.Parse()

	if versionFlag {
		fmt.Printf("curate-mcp v%s\n", Version)
		os.Exit(0)
	}

	// Resolve workspace root from flag, environment, or working directory
	workspace := workspaceFlag
	if workspace == "" {
		workspace = os.Getenv("CURATE_WORKSPACE")
	}
	if workspace == "" {
		var err error
		workspace, err = os.Getwd()
		if err != nil {
			log.Fatalf("failed to resolve current working directory: %v", err)
		}
	}

	workspace, err := filepath.Abs(workspace)
	if err != nil {
		log.Fatalf("failed to resolve absolute path for workspace %s: %v", workspace, err)
	}

	logger := log.New(os.Stderr, "[curate-mcp] ", log.LstdFlags)
	logger.Printf("Initializing curate-mcp v%s for workspace: %s", Version, workspace)

	engine := graph.NewEngine()
	dispatcher := parser.NewDispatcher()

	receiptsPath := mcp.ResolveReceiptsPath(receiptsPathFlag, os.Getenv("CURATE_RECEIPTS_PATH"))

	serverConfig := mcp.ServerConfig{
		WorkspaceRoot: workspace,
		MaxTokens:     maxTokensFlag,
		ReceiptsPath:  receiptsPath,
	}
	server := mcp.NewServer(serverConfig)
	server.SetEngine(engine)

	// Context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		logger.Printf("Received termination signal, shutting down...")
		cancel()
	}()

	// Initial AST indexing of workspace
	initialIndex(workspace, engine, dispatcher, logger)

	// Start file watcher in background
	w := watcher.NewWatcher(watcher.DefaultConfig())
	eventsCh, errsCh, watchErr := w.Start(ctx, workspace)
	if watchErr != nil {
		logger.Printf("Warning: failed to start filesystem watcher: %v", watchErr)
	} else {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case watchErr, ok := <-errsCh:
					if !ok {
						return
					}
					logger.Printf("Watcher error: %v", watchErr)
				case ev, ok := <-eventsCh:
					if !ok {
						return
					}
					handleFileChange(ev, workspace, engine, dispatcher, logger)
				}
			}
		}()
	}

	// RF-6: Memory Autonomy & Idle Purging (60s default timeout)
	idleWatcher := mcp.NewIdleWatcher(60*time.Second, func() {
		runtime.GC()
		debug.FreeOSMemory()
	})
	idleWatcher.Start(ctx)
	defer idleWatcher.Stop()

	// Run MCP JSON-RPC loop over stdio
	runStdioLoop(ctx, server, idleWatcher, os.Stdin, os.Stdout, logger)
}

func initialIndex(root string, engine *graph.Engine, dispatcher *parser.Dispatcher, logger *log.Logger) {
	startTime := time.Now()
	wCfg := watcher.DefaultConfig()
	wHelper := watcher.NewWatcher(wCfg)

	indexedCount := 0
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		if info.IsDir() {
			if wHelper.ShouldIgnore(path) {
				return filepath.SkipDir
			}
			return nil
		}

		if wHelper.ShouldIgnore(path) {
			return nil
		}

		relPath, relErr := filepath.Rel(root, path)
		if relErr != nil {
			relPath = path
		}
		relPath = filepath.ToSlash(relPath)

		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}

		res, parseErr := dispatcher.Parse(path, content)
		if parseErr != nil {
			return nil
		}

		// Add File Node
		fileNode := graph.Node{
			ID:       relPath,
			Label:    filepath.Base(relPath),
			FilePath: relPath,
			Type:     graph.NodeFile,
			Imports:  res.Imports,
		}
		_ = engine.AddNode(fileNode)

		// Add Symbol Nodes
		for _, sym := range res.Symbols {
			symID := fmt.Sprintf("%s:%s", relPath, sym.Name)
			symCalls := sym.Calls
			if len(symCalls) == 0 && len(res.Symbols) == 1 && len(res.Calls) > 0 {
				symCalls = res.Calls
			}
			symNode := graph.Node{
				ID:       symID,
				Label:    sym.Name,
				FilePath: relPath,
				Type:     graph.NodeSymbol,
				Imports:  res.Imports,
				Calls:    symCalls,
			}
			_ = engine.AddNode(symNode)

			// Edge File -> Symbol (defines)
			_ = engine.AddEdge(graph.Edge{
				From: relPath,
				To:   symID,
				Type: graph.EdgeDefines,
			})
		}

		indexedCount++
		return nil
	})

	// Phase 2: Resolve cross-module functional edges (EdgeImports and EdgeCalls) in RAM
	if err := engine.ResolveCrossModuleEdges(); err != nil {
		logger.Printf("Warning: failed to resolve cross-module edges during initial index: %v", err)
	}

	// Phase 2.5: Inject dynamic roots from manifests (RF-2)
	if err := graph.InjectDynamicRoots(root, engine); err != nil {
		logger.Printf("Warning: failed to inject dynamic roots during initial index: %v", err)
	}

	logger.Printf("Initial index completed in %v: %d files indexed, %d graph nodes, %d edges",
		time.Since(startTime), indexedCount, engine.NodeCount(), engine.EdgeCount())
}

func handleFileChange(ev watcher.FileEvent, root string, engine *graph.Engine, dispatcher *parser.Dispatcher, logger *log.Logger) {
	relPath, err := filepath.Rel(root, ev.Path)
	if err != nil {
		relPath = ev.Path
	}
	relPath = filepath.ToSlash(relPath)

	if ev.Op == watcher.OpRemove {
		logger.Printf("File removed: %s", relPath)
		return
	}

	content, err := os.ReadFile(ev.Path)
	if err != nil {
		return
	}

	res, err := dispatcher.Parse(ev.Path, content)
	if err != nil {
		return
	}

	fileNode := graph.Node{
		ID:       relPath,
		Label:    filepath.Base(relPath),
		FilePath: relPath,
		Type:     graph.NodeFile,
		Imports:  res.Imports,
	}
	_ = engine.AddNode(fileNode)

	for _, sym := range res.Symbols {
		symID := fmt.Sprintf("%s:%s", relPath, sym.Name)
		symCalls := sym.Calls
		if len(symCalls) == 0 && len(res.Symbols) == 1 && len(res.Calls) > 0 {
			symCalls = res.Calls
		}
		symNode := graph.Node{
			ID:       symID,
			Label:    sym.Name,
			FilePath: relPath,
			Type:     graph.NodeSymbol,
			Imports:  res.Imports,
			Calls:    symCalls,
		}
		_ = engine.AddNode(symNode)
		_ = engine.AddEdge(graph.Edge{
			From: relPath,
			To:   symID,
			Type: graph.EdgeDefines,
		})
	}

	// Update Phase 2 cross-module functional edges
	if err := engine.ResolveCrossModuleEdges(); err != nil {
		logger.Printf("Warning: failed to resolve cross-module edges on file change: %v", err)
	}

	// Update Phase 2.5 dynamic roots if manifest changed (RF-2)
	baseName := filepath.Base(ev.Path)
	if baseName == "package.json" || baseName == "mcp_registry.json" || baseName == "hooks.json" {
		if err := graph.InjectDynamicRoots(root, engine); err != nil {
			logger.Printf("Warning: failed to re-inject dynamic roots on %s change: %v", baseName, err)
		}
	}

	logger.Printf("Updated graph for %s (%d symbols extracted)", relPath, len(res.Symbols))
}

func runStdioLoop(ctx context.Context, server *mcp.Server, idleWatcher *mcp.IdleWatcher, r io.Reader, w io.Writer, logger *log.Logger) {
	scanner := bufio.NewScanner(r)
	encoder := json.NewEncoder(w)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}

		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		if idleWatcher != nil {
			idleWatcher.RecordActivity()
		}

		var req jsonrpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			_ = encoder.Encode(jsonrpcResponse{
				JSONRPC: "2.0",
				Error: &rpcError{
					Code:    -32700,
					Message: "Parse error",
				},
			})
			continue
		}

		handleRPC(ctx, server, req, encoder, logger)
	}
}

func handleRPC(ctx context.Context, server *mcp.Server, req jsonrpcRequest, enc *json.Encoder, logger *log.Logger) {
	switch req.Method {
	case "initialize":
		res := jsonrpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"serverInfo": map[string]interface{}{
					"name":    "curate-mcp",
					"version": Version,
				},
				"capabilities": map[string]interface{}{
					"tools": map[string]interface{}{},
				},
			},
		}
		_ = enc.Encode(res)

	case "notifications/initialized":
		// No-op for initialized notification

	case "ping":
		_ = enc.Encode(jsonrpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]interface{}{},
		})

	case "tools/list":
		tools := []map[string]interface{}{
			{
				"name":        ToolCurateSlice,
				"description": "Performs fast, in-memory structural AST curation and returns a token-bounded sub-graph slice for an intent query.",
				"inputSchema": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"intent": map[string]interface{}{
							"type":        "string",
							"description": "Natural language or technical description of the intended task or feature",
						},
						"max_files": map[string]interface{}{
							"type":        "integer",
							"description": "Maximum number of primary files to include in slice (default 10)",
						},
						"workspace": map[string]interface{}{
							"type":        "string",
							"description": "Optional workspace root path (validated against directory traversal)",
						},
					},
					"required": []string{"intent"},
				},
			},
			{
				"name":        ToolGetBlastRadius,
				"description": "Calculates blast radius, upstream callers, downstream callees, and criticality Ic for a code symbol in RAM.",
				"inputSchema": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"node_name": map[string]interface{}{
							"type":        "string",
							"description": "Identifier of the code symbol (e.g. 'pkg/auth/auth.go:AuthService')",
						},
						"depth": map[string]interface{}{
							"type":        "integer",
							"description": "BFS traversal depth (1-5, default 2)",
						},
						"max_nodes": map[string]interface{}{
							"type":        "integer",
							"description": "Maximum number of nodes to traverse (default 50, max 200)",
						},
						"persist_receipt": map[string]interface{}{
							"type":        "boolean",
							"description": "Whether to persist an OTel receipt to disk (requires receipts path configured)",
						},
					},
					"required": []string{"node_name"},
				},
			},
			{
				"name":        ToolFindPath,
				"description": "Finds the shortest directed functional path (calls, imports) between two symbols in RAM.",
				"inputSchema": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"node_a": map[string]interface{}{
							"type":        "string",
							"description": "Source node identifier",
						},
						"node_b": map[string]interface{}{
							"type":        "string",
							"description": "Target node identifier",
						},
					},
					"required": []string{"node_a", "node_b"},
				},
			},
			{
				"name":        ToolGetArchitectureSummary,
				"description": "Provides macro architecture metrics and Top 10 God Nodes (high-centrality functional symbols).",
				"inputSchema": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"format": map[string]interface{}{
							"type":        "string",
							"enum":        []string{"json", "markdown"},
							"description": "Output format: 'json' (default, ultralight <= 250B) or 'markdown' (formatted report <= 4KB)",
						},
					},
				},
			},
		}
		_ = enc.Encode(jsonrpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]interface{}{
				"tools": tools,
			},
		})

	case "tools/call":
		var callParams struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &callParams); err != nil {
			_ = enc.Encode(jsonrpcResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error: &rpcError{
					Code:    -32602,
					Message: fmt.Sprintf("Invalid params: %v", err),
				},
			})
			return
		}

		switch callParams.Name {
		case ToolCurateSlice:
			intent, _ := callParams.Arguments["intent"].(string)
			maxFiles := 10
			if mf, ok := callParams.Arguments["max_files"].(float64); ok {
				maxFiles = int(mf)
			}
			ws, _ := callParams.Arguments["workspace"].(string)

			sliceReq := mcp.CurateSliceRequest{
				Intent:    intent,
				MaxFiles:  maxFiles,
				Workspace: ws,
			}

			sliceRes, err := server.CurateSlice(ctx, sliceReq)
			if err != nil {
				_ = enc.Encode(jsonrpcResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Error: &rpcError{
						Code:    -32000,
						Message: err.Error(),
					},
				})
				return
			}

			jsonBytes, _ := marshalIndentWithPool(sliceRes)
			_ = enc.Encode(jsonrpcResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]interface{}{
					"content": []map[string]interface{}{
						{
							"type": "text",
							"text": string(jsonBytes),
						},
					},
				},
			})

		case ToolGetBlastRadius:
			nodeName, _ := callParams.Arguments["node_name"].(string)
			depth := 0
			if d, ok := callParams.Arguments["depth"].(float64); ok {
				depth = int(d)
			}
			maxNodes := 0
			if mn, ok := callParams.Arguments["max_nodes"].(float64); ok {
				maxNodes = int(mn)
			}
			var persistReceipt *bool
			if pr, ok := callParams.Arguments["persist_receipt"].(bool); ok {
				persistReceipt = &pr
			}

			brReq := mcp.BlastRadiusRequest{
				NodeName:       nodeName,
				Depth:          depth,
				MaxNodes:       maxNodes,
				PersistReceipt: persistReceipt,
			}

			brRes, err := server.GetBlastRadius(ctx, brReq)
			if err != nil {
				code := -32000
				var data interface{}
				if rpcErr, ok := err.(*mcp.RPCError); ok {
					code = rpcErr.Code
					data = rpcErr.Data
				}
				_ = enc.Encode(jsonrpcResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Error: &rpcError{
						Code:    code,
						Message: err.Error(),
						Data:    data,
					},
				})
				return
			}

			jsonBytes, _ := marshalIndentWithPool(brRes)
			_ = enc.Encode(jsonrpcResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]interface{}{
					"content": []map[string]interface{}{
						{
							"type": "text",
							"text": string(jsonBytes),
						},
					},
				},
			})

		case ToolFindPath:
			nodeA, _ := callParams.Arguments["node_a"].(string)
			nodeB, _ := callParams.Arguments["node_b"].(string)

			fpReq := mcp.FindPathRequest{
				NodeA: nodeA,
				NodeB: nodeB,
			}

			fpRes, err := server.FindPath(ctx, fpReq)
			if err != nil {
				code := -32000
				var data interface{}
				if rpcErr, ok := err.(*mcp.RPCError); ok {
					code = rpcErr.Code
					data = rpcErr.Data
				}
				_ = enc.Encode(jsonrpcResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Error: &rpcError{
						Code:    code,
						Message: err.Error(),
						Data:    data,
					},
				})
				return
			}

			jsonBytes, _ := marshalIndentWithPool(fpRes)
			_ = enc.Encode(jsonrpcResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]interface{}{
					"content": []map[string]interface{}{
						{
							"type": "text",
							"text": string(jsonBytes),
						},
					},
				},
			})

		case ToolGetArchitectureSummary:
			format, _ := callParams.Arguments["format"].(string)
			sumRes, err := server.GetArchitectureSummaryFormatted(ctx, mcp.ArchitectureSummaryRequest{
				Format: format,
			})
			if err != nil {
				code := -32000
				var data interface{}
				if rpcErr, ok := err.(*mcp.RPCError); ok {
					code = rpcErr.Code
					data = rpcErr.Data
				}
				_ = enc.Encode(jsonrpcResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Error: &rpcError{
						Code:    code,
						Message: err.Error(),
						Data:    data,
					},
				})
				return
			}

			outputContent := sumRes.Raw
			if sumRes.Format == "markdown" && sumRes.Markdown != "" {
				outputContent = sumRes.Markdown
			}

			_ = enc.Encode(jsonrpcResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]interface{}{
					"content": []map[string]interface{}{
						{
							"type": "text",
							"text": outputContent,
						},
					},
				},
			})

		default:
			_ = enc.Encode(jsonrpcResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error: &rpcError{
					Code:    -32601,
					Message: fmt.Sprintf("Tool not found: %s", callParams.Name),
				},
			})
			return
		}

	default:
		if req.ID != nil {
			_ = enc.Encode(jsonrpcResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error: &rpcError{
					Code:    -32601,
					Message: fmt.Sprintf("Method not found: %s", req.Method),
				},
			})
		}
	}
}
