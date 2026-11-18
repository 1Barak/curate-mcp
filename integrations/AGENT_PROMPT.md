# Agent Behavior Directives: curate-mcp

## 🎯 Purpose
`curate-mcp` delivers real-time, in-memory AST structural codebase curation for AI coding agents (Claude Code, Cursor, Windsurf, Antigravity). Instead of topology-blind vector searches or massive file dumps, `curate-mcp` maintains an in-memory directed dependency graph (`calls`, `defines`, `imports`) and provides high-signal, token-capped subgraphs in sub-millisecond response times.

---

## 🛠️ MCP Tool Reference

### 1. `curate_slice`
Extracts a focused AST subgraph relevant to a specified task or intent, respecting an adaptive token budget (≤ 1,000 to 3,500 tokens) with structural diagnostics.

```json
{
  "name": "curate_slice",
  "arguments": {
    "intent": "Natural language task or symbol description (e.g. 'auth middleware and JWT validation')",
    "max_files": 5,
    "offset_files": 0
  }
}
```

**When to invoke:**
- **Task Initiation / Architecture Exploration:** Always invoke `curate_slice` before editing unfamiliar modules to discover primary files and upstream entrypoints (`routes`, `controllers`, `handlers`, `api`, `cli`, `main`).
- **Pagination:** If `has_more: true` is returned, use `offset_files` with `next_offset_files` to retrieve subsequent batches.
- **Structural Diagnostics:** Inspect `structural_diagnostics` for `unused_imports` and `dead_symbols` (`in_degree = 0`).

---

### 2. `get_blast_radius`
Calculates upstream callers `P(u)`, downstream callees `S(u)`, and the Criticality Index (`I_c(u) = 3 * in_degree + out_degree`) to evaluate regression risk before modifying a symbol or file.

```json
{
  "name": "get_blast_radius",
  "arguments": {
    "node_name": "Symbol or file identifier (supports suffix matching, e.g. 'TokenVerifier')",
    "depth": 2,
    "max_nodes": 50
  }
}
```

**When to invoke:**
- **Pre-Refactor Impact Analysis:** Before changing any function signature, struct, or class to identify all callers that must be updated.
- **Risk Assessment:** Review the returned `risk` tier (`BAJO`, `MEDIO`, `ALTO`, `CRÍTICO`) to determine required test coverage.

---

### 3. `find_path`
Discovers the exact, shortest directed functional call/import chain between two symbols or files.

```json
{
  "name": "find_path",
  "arguments": {
    "node_a": "Source symbol or file path",
    "node_b": "Target symbol or file path"
  }
}
```

**When to invoke:**
- **Trace Execution Flows:** Trace how an entrypoint connects to a deep business logic or database layer without guessing or grepping.

---

### 4. `get_architecture_summary`
Provides instant topological cardinality metrics and Top God Nodes.

```json
{
  "name": "get_architecture_summary",
  "arguments": {
    "format": "json"
  }
}
```

**Formats:**
- `"json"` *(default)*: Ultra-compact JSON (≤ 310 bytes, ~78 tokens) for minimal context usage.
- `"markdown"`: Executive Markdown report (≤ 830 bytes) with Top 5 God Nodes by degree.

---

## ⚡ Agent Best Practices
1. **Never guess dependencies:** Rely on `curate_slice` and `find_path` for verified AST connections.
2. **Handle suggestions on -32602:** If a symbol is ambiguous or not found, inspect the `suggestions` array and retry using an exact symbol ID.
3. **Preserve Context Window:** Use `format: "json"` for summaries and keep `max_files` ≤ 5 for surgical efficiency.
