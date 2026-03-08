package engine

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/hotreload/internal/process"
	"github.com/hotreload/internal/watcher"
)

// FileWatcher abstracts the watcher for testability.
type FileWatcher interface {
	Start(ctx context.Context) error
	Close() error
	WatchedDirCount() int
	EventsChan() <-chan string
}

// ProcessManager abstracts process management for testability.
type ProcessManager interface {
	Build(ctx context.Context, buildCmd string) error
	Start(execCmd string) error
	Stop() error
	IsRunning() bool
}

// Config holds the configuration for the hot reload engine.
type Config struct {
	Root       string
	BuildCmd   string
	ExecCmd    string
	DebounceMs int
	Extensions string // comma-separated extensions like ".go,.html"
}

// Engine coordinates watching, building, and restarting.
type Engine struct {
	cfg     Config
	logger  *slog.Logger
	watcher FileWatcher
	proc    ProcessManager

	// Build serialization: ensures only one buildAndRestart runs at a time.
	buildMu sync.Mutex

	// Crash loop detection
	crashMu        sync.Mutex
	recentCrashes  []time.Time
	crashThreshold int           // max crashes in the window
	crashWindow    time.Duration // time window to count crashes
	crashCooldown  time.Duration // cooldown after too many crashes
}

// New creates a new Engine with the given configuration.
func New(cfg Config, logger *slog.Logger) (*Engine, error) {
	extensions := parseExtensions(cfg.Extensions)

	w, err := watcher.New(cfg.Root, extensions, logger)
	if err != nil {
		return nil, err
	}

	return &Engine{
		cfg:            cfg,
		logger:         logger,
		watcher:        w,
		proc:           process.New(logger),
		crashThreshold: 5,
		crashWindow:    10 * time.Second,
		crashCooldown:  3 * time.Second,
	}, nil
}

// Run starts the engine: performs initial build+start, then watches for changes.
func (e *Engine) Run(ctx context.Context) error {
	defer e.cleanup()

	// Start the file watcher
	if err := e.watcher.Start(ctx); err != nil {
		return err
	}
	defer e.watcher.Close()

	e.logger.Info("hotreload started",
		"root", e.cfg.Root,
		"build", e.cfg.BuildCmd,
		"exec", e.cfg.ExecCmd,
		"debounce_ms", e.cfg.DebounceMs,
		"watched_dirs", e.watcher.WatchedDirCount(),
	)

	// Trigger first build immediately (in a goroutine so ctx.Done is responsive)
	var buildCancel context.CancelFunc
	{
		var buildCtx context.Context
		buildCtx, buildCancel = context.WithCancel(ctx)
		go e.buildAndRestart(buildCtx)
	}

	// Debounce timer
	debounce := time.Duration(e.cfg.DebounceMs) * time.Millisecond
	var timer *time.Timer
	var timerC <-chan time.Time // nil channel blocks forever

	for {
		select {
		case <-ctx.Done():
			if buildCancel != nil {
				buildCancel()
			}
			return nil

		case _, ok := <-e.watcher.EventsChan():
			if !ok {
				return nil
			}

			// Cancel any in-progress build when new change arrives
			if buildCancel != nil {
				buildCancel()
				buildCancel = nil
			}

			// Reset debounce timer
			if timer != nil {
				timer.Stop()
			}
			timer = time.NewTimer(debounce)
			timerC = timer.C

		case <-timerC:
			timerC = nil
			timer = nil

			// Check crash loop protection (non-blocking, uses a timer in select)
			if e.inCrashLoop() {
				e.logger.Warn("crash loop detected, cooling down",
					"cooldown", e.crashCooldown)
				// Use a timer so we can still respond to ctx.Done during cooldown
				cooldownTimer := time.NewTimer(e.crashCooldown)
				select {
				case <-cooldownTimer.C:
					// Cooldown complete, proceed with build
				case <-ctx.Done():
					cooldownTimer.Stop()
					return nil
				}
			}

			// Create a cancellable context for this build cycle
			var buildCtx context.Context
			buildCtx, buildCancel = context.WithCancel(ctx)
			go e.buildAndRestart(buildCtx)
		}
	}
}

// buildAndRestart stops the old server, rebuilds, and starts the new server.
// It acquires buildMu to ensure only one build cycle runs at a time.
func (e *Engine) buildAndRestart(ctx context.Context) {
	// Serialize build cycles: wait for any previous build to finish
	e.buildMu.Lock()
	defer e.buildMu.Unlock()

	// Check if already cancelled before starting work
	if ctx.Err() != nil {
		return
	}

	// Stop the previous server first
	if err := e.proc.Stop(); err != nil {
		e.logger.Warn("error stopping previous server", "error", err)
	}

	// Run the build
	if err := e.proc.Build(ctx, e.cfg.BuildCmd); err != nil {
		if ctx.Err() != nil {
			e.logger.Info("build cancelled due to new changes")
			return
		}
		e.logger.Error("build failed", "error", err)
		e.recordCrash()
		return
	}

	// Check if context was cancelled during build
	if ctx.Err() != nil {
		e.logger.Info("build cancelled due to new changes")
		return
	}

	// Start the new server
	if err := e.proc.Start(e.cfg.ExecCmd); err != nil {
		e.logger.Error("failed to start server", "error", err)
		e.recordCrash()
		return
	}

	// Monitor for immediate crash (within 2 seconds of start)
	go func() {
		time.Sleep(2 * time.Second)
		if !e.proc.IsRunning() {
			e.logger.Warn("server exited shortly after starting")
			e.recordCrash()
		}
	}()
}

// cleanup stops any running server process.
func (e *Engine) cleanup() {
	e.logger.Info("cleaning up")
	if err := e.proc.Stop(); err != nil {
		e.logger.Warn("error during cleanup", "error", err)
	}
}

// recordCrash records a crash event for crash loop detection.
func (e *Engine) recordCrash() {
	e.crashMu.Lock()
	defer e.crashMu.Unlock()
	e.recentCrashes = append(e.recentCrashes, time.Now())
}

// inCrashLoop checks if we're in a rapid crash loop.
func (e *Engine) inCrashLoop() bool {
	e.crashMu.Lock()
	defer e.crashMu.Unlock()

	cutoff := time.Now().Add(-e.crashWindow)

	// Prune old crashes
	var recent []time.Time
	for _, t := range e.recentCrashes {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	e.recentCrashes = recent

	return len(recent) >= e.crashThreshold
}

func parseExtensions(s string) []string {
	if s == "" {
		return []string{".go"}
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
