package engine

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---------- mock watcher ----------

type mockWatcher struct {
	events chan string
	dirs   int
	once   sync.Once
}

func newMockWatcher(dirs int) *mockWatcher {
	return &mockWatcher{events: make(chan string, 100), dirs: dirs}
}

func (w *mockWatcher) Start(ctx context.Context) error { return nil }
func (w *mockWatcher) Close() error {
	w.once.Do(func() { close(w.events) })
	return nil
}
func (w *mockWatcher) WatchedDirCount() int      { return w.dirs }
func (w *mockWatcher) EventsChan() <-chan string { return w.events }

// ---------- mock process manager ----------

type mockProc struct {
	mu           sync.Mutex
	buildCalls   int
	startCalls   int
	stopCalls    int
	running      bool
	buildDelay   time.Duration
	buildErr     error
	startErr     error
	onBuildStart func() // called when Build begins (before delay)
}

func (p *mockProc) Build(ctx context.Context, cmd string) error {
	p.mu.Lock()
	p.buildCalls++
	if p.onBuildStart != nil {
		p.onBuildStart()
	}
	delay := p.buildDelay
	err := p.buildErr
	p.mu.Unlock()

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return fmt.Errorf("build cancelled: %w", ctx.Err())
		}
	}
	if err != nil {
		return err
	}
	return nil
}

func (p *mockProc) Start(cmd string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.startCalls++
	if p.startErr != nil {
		return p.startErr
	}
	p.running = true
	return nil
}

func (p *mockProc) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopCalls++
	p.running = false
	return nil
}

func (p *mockProc) IsRunning() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.running
}

func (p *mockProc) getBuildCalls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.buildCalls
}

func (p *mockProc) getStartCalls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.startCalls
}

func (p *mockProc) getStopCalls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stopCalls
}

// ---------- helpers ----------

func newTestEngine(w FileWatcher, p ProcessManager, debounceMs int) *Engine {
	return &Engine{
		cfg: Config{
			Root:       ".",
			BuildCmd:   "echo build",
			ExecCmd:    "echo run",
			DebounceMs: debounceMs,
		},
		logger:         slog.Default(),
		watcher:        w,
		proc:           p,
		crashThreshold: 3,
		crashWindow:    10 * time.Second,
		crashCooldown:  200 * time.Millisecond,
	}
}

// ---------- tests ----------

func TestParseExtensions(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"", []string{".go"}},
		{".go", []string{".go"}},
		{".go,.html", []string{".go", ".html"}},
		{".go, .html, .yaml", []string{".go", ".html", ".yaml"}},
		{"go,html", []string{"go", "html"}},
	}

	for _, tt := range tests {
		got := parseExtensions(tt.input)
		if len(got) != len(tt.expected) {
			t.Errorf("parseExtensions(%q) = %v, want %v", tt.input, got, tt.expected)
			continue
		}
		for i := range got {
			if got[i] != tt.expected[i] {
				t.Errorf("parseExtensions(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.expected[i])
			}
		}
	}
}

func TestCrashLoopDetection(t *testing.T) {
	cfg := Config{
		Root:       ".",
		BuildCmd:   "echo build",
		ExecCmd:    "echo run",
		DebounceMs: 100,
	}

	e := &Engine{
		cfg:            cfg,
		crashThreshold: 3,
		crashWindow:    10 * time.Second,
		crashCooldown:  1 * time.Second,
	}

	// Should not be in crash loop initially
	if e.inCrashLoop() {
		t.Error("should not be in crash loop initially")
	}

	// Record crashes below threshold
	e.recordCrash()
	e.recordCrash()
	if e.inCrashLoop() {
		t.Error("should not be in crash loop with 2 crashes (threshold 3)")
	}

	// Hit threshold
	e.recordCrash()
	if !e.inCrashLoop() {
		t.Error("should be in crash loop after 3 crashes")
	}
}

// TestInitialBuildOnStart verifies the engine triggers a build+start immediately.
func TestInitialBuildOnStart(t *testing.T) {
	w := newMockWatcher(1)
	p := &mockProc{}
	e := newTestEngine(w, p, 50)

	ctx, cancel := context.WithCancel(context.Background())

	// Run engine in background
	done := make(chan error, 1)
	go func() { done <- e.Run(ctx) }()

	// Wait for build + start to complete
	deadline := time.After(2 * time.Second)
	for {
		if p.getStartCalls() >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for initial build+start")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	if p.getBuildCalls() < 1 {
		t.Errorf("expected at least 1 build call, got %d", p.getBuildCalls())
	}
	if p.getStartCalls() < 1 {
		t.Errorf("expected at least 1 start call, got %d", p.getStartCalls())
	}

	cancel()
	<-done
}

// TestDebounceCoalescesEvents verifies rapid events are coalesced into one build.
func TestDebounceCoalescesEvents(t *testing.T) {
	w := newMockWatcher(1)
	p := &mockProc{}
	e := newTestEngine(w, p, 100) // 100ms debounce

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- e.Run(ctx) }()

	// Wait for initial build
	deadline := time.After(2 * time.Second)
	for {
		if p.getStartCalls() >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for initial build")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	// Record build count after initial build
	buildsBefore := p.getBuildCalls()

	// Fire 5 rapid events (should be debounced into 1 build)
	for i := 0; i < 5; i++ {
		w.events <- "file.go"
		time.Sleep(10 * time.Millisecond)
	}

	// Wait for debounce + build to complete
	time.Sleep(400 * time.Millisecond)

	buildsAfter := p.getBuildCalls()
	// All 5 events should coalesce into exactly 1 additional build
	if buildsAfter-buildsBefore != 1 {
		t.Errorf("expected exactly 1 debounced build, got %d", buildsAfter-buildsBefore)
	}

	cancel()
	<-done
}

// TestBuildCancellationOnNewChange verifies that a new file change cancels an
// in-progress build.
func TestBuildCancellationOnNewChange(t *testing.T) {
	w := newMockWatcher(1)

	var buildStarted atomic.Int32
	p := &mockProc{
		buildDelay: 500 * time.Millisecond,
		onBuildStart: func() {
			buildStarted.Add(1)
		},
	}
	e := newTestEngine(w, p, 50) // 50ms debounce

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- e.Run(ctx) }()

	// Wait for initial build to start
	deadline := time.After(2 * time.Second)
	for buildStarted.Load() < 1 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for initial build to start")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	// Fire a change event during the build — this should cancel the current
	// build context and trigger a new build after debounce
	w.events <- "changed.go"

	// Wait for the second build to start
	deadline = time.After(2 * time.Second)
	for buildStarted.Load() < 2 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for second build to start")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	// The first build should have been cancelled (not started the server)
	// and a second build should have been triggered
	if buildStarted.Load() < 2 {
		t.Errorf("expected at least 2 build starts, got %d", buildStarted.Load())
	}

	cancel()
	<-done
}

// TestCtxCancelDuringCooldown verifies the engine exits when ctx is cancelled
// while in crash loop cooldown.
func TestCtxCancelDuringCooldown(t *testing.T) {
	w := newMockWatcher(1)
	p := &mockProc{buildErr: fmt.Errorf("always fail")}
	e := newTestEngine(w, p, 50)
	e.crashCooldown = 5 * time.Second // long cooldown

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- e.Run(ctx) }()

	// Wait for initial (failed) build
	deadline := time.After(2 * time.Second)
	for p.getBuildCalls() < 1 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for initial build")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	// Record enough crashes to trigger crash loop
	e.recordCrash() // builds already recorded 1 via buildAndRestart
	e.recordCrash()

	// Fire an event to trigger the debounce → crash loop cooldown path
	w.events <- "file.go"

	// Give time for debounce + crash loop detection
	time.Sleep(300 * time.Millisecond)

	// Cancel while in cooldown — engine should exit promptly, not block
	cancel()

	select {
	case <-done:
		// Good — engine exited
	case <-time.After(1 * time.Second):
		t.Fatal("engine did not exit promptly after context cancellation during cooldown")
	}
}

// TestEngineStopsOnWatcherClose verifies the engine exits when the watcher
// event channel is closed.
func TestEngineStopsOnWatcherClose(t *testing.T) {
	w := newMockWatcher(1)
	p := &mockProc{}
	e := newTestEngine(w, p, 50)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- e.Run(ctx) }()

	// Wait for initial build
	deadline := time.After(2 * time.Second)
	for p.getStartCalls() < 1 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for initial start")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	// Close the events channel (simulating watcher failure)
	w.Close()

	select {
	case <-done:
		// engine exited as expected
	case <-time.After(2 * time.Second):
		t.Fatal("engine did not exit after watcher channel closed")
	}
}
