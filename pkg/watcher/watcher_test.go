package watcher

import (
	"context"
	"testing"
	"time"
)

// TestWatcher_ShouldIgnore verifies directory and binary filtering.
func TestWatcher_ShouldIgnore(t *testing.T) {
	w := NewWatcher(DefaultConfig())

	tests := []struct {
		path         string
		shouldIgnore bool
	}{
		// Ignored VCS and dependencies
		{".git/objects/a1/b2c3d4", true},
		{".git/HEAD", true},
		{"node_modules/express/index.js", true},
		{"vendor/github.com/pkg/errors/errors.go", true},
		{".vscode/settings.json", true},
		{".idea/workspace.xml", true},

		// Ignored binary artifacts
		{"bin/curate.exe", true},
		{"target/release/libcore.so", true},
		{"build/output.dylib", true},
		{"cmd/tool.bin", true},

		// Valid source files (must NOT be ignored)
		{"pkg/graph/engine.go", false},
		{"src/service/user.py", false},
		{"frontend/src/app.tsx", false},
		{"core/src/lib.rs", false},
		{"native/buffer.cpp", false},
		{"src/main/java/Main.java", false},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			ignored := w.ShouldIgnore(tc.path)
			if ignored != tc.shouldIgnore {
				t.Errorf("ShouldIgnore(%q) = %v; expected %v", tc.path, ignored, tc.shouldIgnore)
			}
		})
	}
}

// TestWatcher_DebounceContract verifies the 75ms debounce configuration invariant.
func TestWatcher_DebounceContract(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.DebounceDuration != 75*time.Millisecond {
		t.Fatalf("debounce invariant violated: expected 75ms, got %v", cfg.DebounceDuration)
	}

	w := NewWatcher(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events, errs, err := w.Start(ctx, ".")
	if err != nil {
		t.Fatalf("watcher Start failed: %v", err)
	}
	if events == nil || errs == nil {
		t.Fatal("watcher channels must not be nil upon start")
	}
}
