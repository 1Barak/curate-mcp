package mcp

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// IdleWatcherConfig holds configuration for the IdleWatcher.
type IdleWatcherConfig struct {
	IdleTimeout time.Duration
	OnPurge     func()
}

// IdleWatcher manages memory autonomy and idle purging (RF-6).
type IdleWatcher struct {
	idleTimeout time.Duration
	idlePurged  atomic.Bool
	purgeFunc   func()
	running     atomic.Bool
	stopped     atomic.Bool

	mu     sync.Mutex
	timer  *time.Timer
	stopCh chan struct{}
	doneCh chan struct{}
}

// NewIdleWatcher initializes a new IdleWatcher with configurable timeout and purge callback.
func NewIdleWatcher(timeout time.Duration, purgeFunc func()) *IdleWatcher {
	return NewIdleWatcherWithConfig(IdleWatcherConfig{
		IdleTimeout: timeout,
		OnPurge:     purgeFunc,
	})
}

// NewIdleWatcherWithConfig initializes a new IdleWatcher from IdleWatcherConfig.
func NewIdleWatcherWithConfig(cfg IdleWatcherConfig) *IdleWatcher {
	return &IdleWatcher{
		idleTimeout: cfg.IdleTimeout,
		purgeFunc:   cfg.OnPurge,
	}
}

// Start begins the background idle monitoring loop.
func (w *IdleWatcher) Start(ctx context.Context) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.running.Swap(true) {
		return
	}
	w.stopped.Store(false)
	w.stopCh = make(chan struct{})
	w.doneCh = make(chan struct{})
	w.timer = time.NewTimer(w.idleTimeout)

	go func() {
		defer close(w.doneCh)
		for {
			select {
			case <-ctx.Done():
				w.mu.Lock()
				w.stopTimerLocked()
				w.mu.Unlock()
				return
			case <-w.stopCh:
				w.mu.Lock()
				w.stopTimerLocked()
				w.mu.Unlock()
				return
			case <-w.timer.C:
				if w.idlePurged.CompareAndSwap(false, true) {
					if w.purgeFunc != nil {
						w.purgeFunc()
					}
				}
			}
		}
	}()
}

// Stop gracefully stops the idle watcher.
func (w *IdleWatcher) Stop() {
	w.mu.Lock()
	if !w.running.Load() || w.stopped.Load() {
		w.mu.Unlock()
		return
	}
	w.stopped.Store(true)
	w.running.Store(false)
	close(w.stopCh)
	w.stopTimerLocked()
	done := w.doneCh
	w.mu.Unlock()

	if done != nil {
		<-done
	}
}

func (w *IdleWatcher) stopTimerLocked() {
	if w.timer != nil {
		if !w.timer.Stop() {
			select {
			case <-w.timer.C:
			default:
			}
		}
	}
}

// RecordActivity signals incoming RPC activity, resetting the idle timer and idlePurged flag.
func (w *IdleWatcher) RecordActivity() {
	if w.stopped.Load() || !w.running.Load() {
		return
	}
	w.idlePurged.Store(false)

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped.Load() || !w.running.Load() {
		return
	}
	if w.timer != nil {
		if !w.timer.Stop() {
			select {
			case <-w.timer.C:
			default:
			}
		}
		w.timer.Reset(w.idleTimeout)
	}
}

// IsPurged returns whether the current idle period has already performed a purge.
func (w *IdleWatcher) IsPurged() bool {
	return w.idlePurged.Load()
}
