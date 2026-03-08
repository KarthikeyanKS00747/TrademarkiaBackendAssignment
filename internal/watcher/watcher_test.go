package watcher

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	w, err := New(dir, []string{".go"}, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer w.Close()

	if w.root == "" {
		t.Error("root should not be empty")
	}

	if !w.extensions[".go"] {
		t.Error("expected .go in extensions map")
	}
}

func TestWatcherDetectsFileCreate(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	w, err := New(dir, []string{".go"}, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer w.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := w.Start(ctx); err != nil {
		t.Fatalf("failed to start watcher: %v", err)
	}

	// Create a .go file
	testFile := filepath.Join(dir, "test.go")
	if err := os.WriteFile(testFile, []byte("package main"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	// Wait for event
	select {
	case path := <-w.Events:
		if filepath.Base(path) != "test.go" {
			t.Errorf("expected test.go, got %s", filepath.Base(path))
		}
	case <-time.After(3 * time.Second):
		t.Error("timed out waiting for file create event")
	}
}

func TestWatcherDetectsFileWrite(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	// Pre-create the file
	testFile := filepath.Join(dir, "main.go")
	if err := os.WriteFile(testFile, []byte("package main\n"), 0644); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}

	w, err := New(dir, []string{".go"}, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer w.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := w.Start(ctx); err != nil {
		t.Fatalf("failed to start watcher: %v", err)
	}

	// Give watcher time to set up
	time.Sleep(100 * time.Millisecond)

	// Modify the file
	if err := os.WriteFile(testFile, []byte("package main\n// modified\n"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	// Wait for event
	select {
	case path := <-w.Events:
		if filepath.Base(path) != "main.go" {
			t.Errorf("expected main.go, got %s", filepath.Base(path))
		}
	case <-time.After(3 * time.Second):
		t.Error("timed out waiting for file write event")
	}
}

func TestWatcherIgnoresNonGoFiles(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	w, err := New(dir, []string{".go"}, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer w.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := w.Start(ctx); err != nil {
		t.Fatalf("failed to start watcher: %v", err)
	}

	// Create a .txt file - should be ignored
	txtFile := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(txtFile, []byte("hello"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	// Should NOT get an event
	select {
	case path := <-w.Events:
		t.Errorf("unexpected event for non-Go file: %s", path)
	case <-time.After(500 * time.Millisecond):
		// Expected: no event
	}
}

func TestWatcherIgnoresTempFiles(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	w, err := New(dir, []string{".go"}, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer w.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := w.Start(ctx); err != nil {
		t.Fatalf("failed to start watcher: %v", err)
	}

	// Create temp files that editors generate
	tempFiles := []string{
		filepath.Join(dir, "main.go~"),
		filepath.Join(dir, ".main.go.swp"),
		filepath.Join(dir, "#main.go#"),
	}

	for _, f := range tempFiles {
		if err := os.WriteFile(f, []byte("temp"), 0644); err != nil {
			t.Fatalf("failed to write temp file %s: %v", f, err)
		}
	}

	// Should NOT get events for temp files
	select {
	case path := <-w.Events:
		t.Errorf("unexpected event for temp file: %s", path)
	case <-time.After(500 * time.Millisecond):
		// Expected: no events
	}
}

func TestWatcherNewSubdirectory(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	w, err := New(dir, []string{".go"}, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer w.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := w.Start(ctx); err != nil {
		t.Fatalf("failed to start watcher: %v", err)
	}

	initialCount := w.WatchedDirCount()

	// Create a new subdirectory
	subDir := filepath.Join(dir, "pkg")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}

	// Give watchers time to pick up the new directory
	time.Sleep(500 * time.Millisecond)

	// Create a .go file inside the new directory
	testFile := filepath.Join(subDir, "helper.go")
	if err := os.WriteFile(testFile, []byte("package pkg"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	// We should get an event for the new file
	select {
	case path := <-w.Events:
		if filepath.Base(path) != "helper.go" {
			t.Errorf("expected helper.go, got %s", filepath.Base(path))
		}
	case <-time.After(3 * time.Second):
		t.Error("timed out waiting for event in new subdirectory")
	}

	// Directory count should have increased
	newCount := w.WatchedDirCount()
	if newCount <= initialCount {
		t.Errorf("expected watched dir count to increase from %d, got %d", initialCount, newCount)
	}
}

func TestShouldIgnoreDir(t *testing.T) {
	w := &Watcher{}

	tests := []struct {
		name     string
		expected bool
	}{
		{".git", true},
		{"node_modules", true},
		{"vendor", true},
		{".idea", true},
		{"bin", true},
		{"src", false},
		{"cmd", false},
		{"internal", false},
		{"pkg", false},
	}

	for _, tt := range tests {
		got := w.shouldIgnoreDir(tt.name)
		if got != tt.expected {
			t.Errorf("shouldIgnoreDir(%q) = %v, want %v", tt.name, got, tt.expected)
		}
	}
}

func TestShouldIgnoreFile(t *testing.T) {
	w := &Watcher{}

	tests := []struct {
		path     string
		expected bool
	}{
		{"main.go", false},
		{"main.go~", true},
		{".main.go.swp", true},
		{"#main.go#", true},
		{"main.tmp", true},
		{"main.bak", true},
		{".hidden", true},
	}

	for _, tt := range tests {
		got := w.shouldIgnoreFile(tt.path)
		if got != tt.expected {
			t.Errorf("shouldIgnoreFile(%q) = %v, want %v", tt.path, got, tt.expected)
		}
	}
}
