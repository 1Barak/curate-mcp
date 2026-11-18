package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

// TestCurateSlice_JSONRPCContract verifies the exact JSON-RPC schema contract of curate_slice.
// Must return: status, project, intent, primary_files, entrypoints, duration_ms.
func TestCurateSlice_JSONRPCContract(t *testing.T) {
	server := NewServer(ServerConfig{
		WorkspaceRoot: "/workspace/project",
		MaxTokens:     1000,
	})

	req := CurateSliceRequest{
		Intent:    "authentication middleware and session tokens",
		MaxFiles:  5,
		Workspace: "/workspace/project",
	}

	res, err := server.CurateSlice(context.Background(), req)
	if err != nil {
		t.Fatalf("CurateSlice invocation failed: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil CurateSliceResponse")
	}

	// 1. Structural assertions
	if res.Status != "ok" {
		t.Errorf("expected status 'ok', got %q", res.Status)
	}
	if res.Project == "" {
		t.Error("expected non-empty project name")
	}
	if res.Intent != req.Intent {
		t.Errorf("expected intent %q, got %q", req.Intent, res.Intent)
	}
	if res.DurationMS < 0 {
		t.Errorf("expected non-negative duration_ms, got %d", res.DurationMS)
	}

	// 2. JSON Serialization Contract check
	jsonBytes, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("failed to marshal response to JSON: %v", err)
	}

	var jsonMap map[string]interface{}
	if err := json.Unmarshal(jsonBytes, &jsonMap); err != nil {
		t.Fatalf("failed to unmarshal JSON into map: %v", err)
	}

	requiredFields := []string{
		"status",
		"project",
		"intent",
		"primary_files",
		"entrypoints",
		"duration_ms",
	}

	for _, field := range requiredFields {
		if _, ok := jsonMap[field]; !ok {
			t.Errorf("schema violation: required JSON field %q missing from payload: %s", field, string(jsonBytes))
		}
	}
}

// TestCurateSlice_PathTraversalDefense verifies defensive security against directory traversal attacks.
func TestCurateSlice_PathTraversalDefense(t *testing.T) {
	server := NewServer(ServerConfig{
		WorkspaceRoot: "/workspace/curate-mcp",
		MaxTokens:     1000,
	})

	maliciousPaths := []struct {
		name string
		path string
	}{
		{"unix relative traversal", "../../etc/passwd"},
		{"deep relative traversal", "../../../../etc/shadow"},
		{"windows relative traversal", `..\..\windows\system32\cmd.exe`},
		{"nested relative escape", "sub/folder/../../../../etc/hosts"},
		{"null byte injection", "valid/path\x00/../../etc/passwd"},
		{"absolute system path outside workspace", "/etc/passwd"},
		{"windows absolute system path", `C:\Windows\System32\drivers\etc\hosts`},
	}

	for _, tc := range maliciousPaths {
		t.Run(tc.name, func(t *testing.T) {
			validatedPath, err := server.ValidateWorkspacePath(tc.path)
			if err == nil {
				t.Fatalf("security violation: path traversal %q was accepted as valid (got %q)",
					tc.path, validatedPath)
			}
			if err != ErrPathTraversal && err != ErrInvalidPath {
				t.Errorf("expected ErrPathTraversal or ErrInvalidPath, got: %v", err)
			}
		})
	}
}
