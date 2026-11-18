package watcher

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

var (
	ErrWatcherNotImplemented = errors.New("watcher: fsnotify debounce loop not implemented")
)

// OpType represents the kind of filesystem mutation event.
type OpType string

const (
	OpCreate OpType = "create"
	OpWrite  OpType = "write"
	OpRemove OpType = "remove"
	OpRename OpType = "rename"
)

// FileEvent represents a debounced filesystem change event.
type FileEvent struct {
	Path      string `json:"path"`
	Op        OpType `json:"op"`
	Timestamp int64  `json:"timestamp"`
}

// Config configures the watcher behavior.
type Config struct {
	DebounceDuration time.Duration
	IgnoredDirs      []string
	SupportedExts    []string
}

// DefaultConfig returns standard defaults: 75ms debounce, standard ignores.
func DefaultConfig() Config {
	return Config{
		DebounceDuration: 75 * time.Millisecond,
		IgnoredDirs: []string{
			".git",
			"node_modules",
			"vendor",
			".idea",
			".vscode",
		},
		SupportedExts: []string{
			".go",
			".py",
			".ts", ".tsx",
			".js", ".jsx", ".mjs", ".cjs",
			".rs",
			".c", ".h", ".cpp", ".hpp", ".cc",
			".java",
		},
	}
}

// Watcher monitors filesystem changes and aggregates rapid bursts via debouncing.
type Watcher struct {
	cfg Config
}

// NewWatcher initializes a new Watcher with the given configuration.
func NewWatcher(cfg Config) *Watcher {
	return &Watcher{cfg: cfg}
}

var binaryExtensions = map[string]bool{
	".exe":   true,
	".so":    true,
	".dylib": true,
	".bin":   true,
	".dll":   true,
	".obj":   true,
	".o":     true,
	".a":     true,
	".class": true,
	".jar":   true,
}

// ShouldIgnore determines if a file path should be ignored (e.g. .git, node_modules, binaries).
func (w *Watcher) ShouldIgnore(filePath string) bool {
	cleanPath := filepath.ToSlash(filePath)
	segments := strings.Split(cleanPath, "/")

	for _, seg := range segments {
		for _, ign := range w.cfg.IgnoredDirs {
			if seg == ign {
				return true
			}
		}
	}

	ext := strings.ToLower(filepath.Ext(cleanPath))
	if binaryExtensions[ext] {
		return true
	}

	return false
}

// Start begins monitoring the target directory and yields debounced events.
func (w *Watcher) Start(ctx context.Context, rootDir string) (<-chan FileEvent, <-chan error, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, nil, err
	}

	// Recursively register directories
	err = filepath.Walk(rootDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if info.IsDir() {
			if w.ShouldIgnore(path) {
				return filepath.SkipDir
			}
			return fsw.Add(path)
		}
		return nil
	})
	if err != nil {
		_ = fsw.Close()
		return nil, nil, err
	}

	eventsCh := make(chan FileEvent, 100)
	errsCh := make(chan error, 50)

	go func() {
		defer func() {
			_ = fsw.Close()
			close(eventsCh)
			close(errsCh)
		}()

		var mu sync.Mutex
		pendingTimers := make(map[string]*time.Timer)
		pendingOps := make(map[string]OpType)

		for {
			select {
			case <-ctx.Done():
				mu.Lock()
				for _, t := range pendingTimers {
					t.Stop()
				}
				mu.Unlock()
				return

			case err, ok := <-fsw.Errors:
				if !ok {
					return
				}
				select {
				case errsCh <- err:
				default:
				}

			case ev, ok := <-fsw.Events:
				if !ok {
					return
				}

				if w.ShouldIgnore(ev.Name) {
					continue
				}

				var op OpType
				switch {
				case ev.Has(fsnotify.Create):
					op = OpCreate
					// If a new directory is created, register it
					if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
						_ = fsw.Add(ev.Name)
					}
				case ev.Has(fsnotify.Write):
					op = OpWrite
				case ev.Has(fsnotify.Remove):
					op = OpRemove
				case ev.Has(fsnotify.Rename):
					op = OpRename
				default:
					continue
				}

				path := ev.Name

				mu.Lock()
				if timer, exists := pendingTimers[path]; exists {
					timer.Stop()
				}
				pendingOps[path] = op

				capturedPath := path
				pendingTimers[path] = time.AfterFunc(w.cfg.DebounceDuration, func() {
					mu.Lock()
					finalOp := pendingOps[capturedPath]
					delete(pendingTimers, capturedPath)
					delete(pendingOps, capturedPath)
					mu.Unlock()

					fileEvent := FileEvent{
						Path:      capturedPath,
						Op:        finalOp,
						Timestamp: time.Now().UnixMilli(),
					}

					select {
					case eventsCh <- fileEvent:
					case <-ctx.Done():
					}
				})
				mu.Unlock()
			}
		}
	}()

	return eventsCh, errsCh, nil
}
