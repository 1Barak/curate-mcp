# curate-mcp

[![Go Version](https://img.shields.io/badge/Go-1.26+-00ADD8?style=flat&logo=go)](https://golang.org)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)
[![Platform](https://img.shields.io/badge/Platform-Linux%20%7C%20macOS%20%7C%20Windows-lightgrey.svg)]()
[![Zero Dependencies](https://img.shields.io/badge/Dependencies-Zero%20Runtime-green.svg)]()

> **Fast, lightweight, native Go Model Context Protocol (MCP) server for real-time AST dependency analysis and surgical codebase curation.**

`curate-mcp` maintains an in-memory directed dependency graph of your codebase, continuously synchronized via reactive file watching. When AI coding agents (Claude Code, Cursor, Windsurf, Antigravity) request context, `curate-mcp` delivers high-signal, token-capped AST subgraphs in sub-millisecond response times.

---

## ⚡ Key Highlights

- **Sub-millisecond Performance:** Queries resolve in $< 1\text{ ms}$ directly from memory via concurrent `sync.RWMutex` adjacency maps.
- **Single Static Binary (Zero Runtime Dependencies):** No Python virtual environments, no Node.js runtime, no Docker containers, and no external graph databases (Neo4j/Memgraph). Pure static Go (`CGO_ENABLED=0`).
- **Multi-Language AST Support (7 Core Languages):** Native parsing for **Go**, **Python**, **TypeScript/TSX**, **JavaScript/JSX**, **Rust**, **C/C++**, and **Java**.
- **Real-Time Reactive Sync:** Multi-platform file watcher (`fsnotify`) with 75 ms debouncing absorbs I/O bursts and updates the in-memory graph immediately upon file changes.
- **Context Shield (Strict Token Preservation):** Multi-field weighted scoring (`label*2.0 + path*1.5 + imports*1.0`) combined with deterministic 3-stage cascading pruning (`symbols` $\rightarrow$ `entrypoints` $\rightarrow$ `primary_files`) guarantees a strict Hard Cap ($\le 1,000$ tokens).
- **Security-First Architecture:** Active defense against Path Traversal and workspace escape vectors.

---

## 🏗️ Architecture

```mermaid
flowchart LR
    FS["File System\n(Linux / macOS / Windows)"] -->|Events| Watcher["fsnotify Dispatcher\n(75ms Debounce)"]
    Watcher -->|Changed Files| Parser["7-Language AST Dispatcher\n(Go, Py, TS, JS, Rs, C++, Java)"]
    Parser -->|Definitions & Imports| Engine["In-Memory Directed Graph\n(sync.RWMutex in RAM)"]
    
    Agent["AI Coding Agent\n(Claude, Cursor, Windsurf, Antigravity)"] -->|curate_slice(intent)| Server["MCP Server (stdio)"]
    Server -->|Query| Scorer["Multi-Field Scorer\n(2.0*Label + 1.5*Path + 1.0*Imports)"]
    Scorer -->|Sub-graph| Pruner["Deterministic Pruner\n(Hard Cap <= 1,000 tok)"]
    Pruner -->|Surgical JSON Payload| Server
    Server -->|Sub-millisecond Response| Agent
```

---

## 📦 Installation & Build

### Prerequisites
- [Go 1.26+](https://golang.org/dl/) (or Go 1.22+)

### Build from Source
```bash
git clone https://github.com/<your-username>/curate-mcp.git
cd curate-mcp

# Build native static executable
go build -o bin/curate ./cmd/curate
```

The resulting standalone binary in `bin/curate` (or `bin/curate.exe` on Windows) is $< 5\text{ MB}$ and ready to run.

---

## 🚀 CLI Usage

```bash
# Run server using current directory as workspace
./bin/curate

# Run server with an explicit workspace
./bin/curate -workspace /path/to/project
# or with shorthand
./bin/curate -w /path/to/project

# Set custom hard token limit (default: 1000)
./bin/curate -w /path/to/project -max-tokens 800

# Print version
./bin/curate -version
```

### Dynamic Workspace Discovery
`curate-mcp` resolves the target codebase workspace in the following priority:
1. CLI flag: `-w` / `--workspace <path>`
2. Environment variable: `CURATE_WORKSPACE=<path>`
3. Current working directory: `os.Getwd()`

---

## 🔌 Agent Integrations

Pre-configured integration templates are available in the [`integrations/`](integrations/) directory.

### 1. Claude Desktop
Add to your `claude_desktop_config.json`:
```json
{
  "mcpServers": {
    "curate": {
      "command": "/absolute/path/to/bin/curate",
      "args": ["-w", "/path/to/your/workspace"]
    }
  }
}
```

### 2. Cursor
Add to `.cursor/mcp.json`:
```json
{
  "mcpServers": {
    "curate": {
      "command": "/absolute/path/to/bin/curate",
      "args": ["-w", "${workspaceFolder}"]
    }
  }
}
```

### 3. Windsurf
Add to `mcp_config.json`:
```json
{
  "mcpServers": {
    "curate": {
      "command": "/absolute/path/to/bin/curate",
      "args": ["-w", "${workspaceFolder}"]
    }
  }
}
```

### 4. Antigravity
Add to your MCP configuration:
```json
{
  "mcpServers": {
    "curate": {
      "command": "/absolute/path/to/bin/curate",
      "args": ["-w", "${workspaceFolder}"]
    }
  }
}
```

---

## 🛠️ MCP Tool Reference

### `curate_slice`
Extracts a focused AST sub-graph relevant to a specified task or user intent.

**Parameters:**
- `intent` *(string, required)*: Natural language description of what you are searching for or working on (e.g., `"user authentication middleware and jwt tokens"`).
- `max_files` *(integer, optional)*: Maximum number of primary files to retrieve (default: `5`).
- `workspace` *(string, optional)*: Path to the workspace (defaults to configured root).

---

### `get_blast_radius`
Calculates upstream callers $P(u)$, downstream callees $S(u)$, and the Criticality Index ($I_c$) for a given symbol or file before applying changes.

**Parameters:**
- `node_name` *(string, required)*: Target symbol or file identifier (e.g., `"pkg/graph/engine.go"` or `"NewEngine"`).
- `depth` *(integer, optional)*: BFS traversal depth limit (default: `2`, max: `5`).
- `max_nodes` *(integer, optional)*: Maximum number of nodes to extract (default: `50`, max: `200`).
- `persist_receipt` *(boolean, optional)*: Explicitly toggle OTel receipt emission to disk.

---

### `find_path`
Discovers the shortest directed dependency path between two symbols or files via functional calls and imports.

**Parameters:**
- `node_a` *(string, required)*: Origin symbol or file identifier.
- `node_b` *(string, required)*: Destination symbol or file identifier.

---

### `get_architecture_summary`
Provides instant topological cardinality metrics and ranks the Top 10 critical *God Nodes* by functional degree.

**Parameters:** None.

**Output:**
```json
{
  "status": "ok",
  "project": "my-service",
  "intent": "user authentication middleware and jwt tokens",
  "primary_files": [
    {
      "path": "src/auth/token.go",
      "language": "go",
      "score": 8.5,
      "symbols": [
        {"name": "GenerateToken", "kind": "function", "start_line": 24},
        {"name": "TokenVerifier", "kind": "struct", "start_line": 45}
      ]
    }
  ],
  "entrypoints": [
    {
      "path": "cmd/api/server.go",
      "role": "upstream_caller"
    }
  ],
  "duration_ms": 0
}
```

---

## 🧪 Testing

Run the comprehensive unit and integration test suite:

```bash
go test -v ./...
```

All tests execute in $< 50\text{ ms}$ with zero dependencies.

---

## 🗺️ Roadmap & Community Feedback

`curate-mcp` is under active development. We are prioritizing upcoming features based directly on community demand:

- [ ] **Massive Monorepos (>100k files):** Hybrid in-memory + embedded persistent indexing (SQLite/DuckDB) for instant warm boots on giant codebases.
- [ ] **Cross-Repository & Microservices:** Graph stitching across distributed services (Protobuf, gRPC, OpenAPI contracts).
- [ ] **CI/CD GitHub Action:** Automated Pull Request blast radius & AST impact analysis for ultra-low-token AI code reviews.
- [ ] **Additional Languages:** C#, Kotlin, Swift, PHP, and SQL schema mappings.
- [ ] **Interactive Visual Graph Inspector:** Local lightweight web UI to visually navigate dependency subgraphs.

### 💬 Share Your Use Case!
- Are you working with an enterprise monorepo or a large distributed team?
- Need a specific language grammar or custom agent integration?

👉 **[Open an Issue](https://github.com/curate-mcp/curate-mcp/issues)** or start a **Discussion** sharing your use case. Community feedback directly shapes our development priorities!

---

## 📄 License & Attribution

- Licensed under the **[Apache License, Version 2.0](LICENSE)**.
- See **[NOTICE](NOTICE)**: Architectural concepts regarding in-memory AST and graph-based codebase curation are inspired in part by [Graphify](https://github.com/Graphify-Labs/graphify) (Apache-2.0, Copyright Graphify-Labs Authors).
