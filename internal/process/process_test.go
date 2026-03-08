package process

import (
	"context"
	"log/slog"
	"os"
	"runtime"
	"testing"
	"time"
)

func TestBuildSuccess(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	m := New(logger)

	var cmd string
	if runtime.GOOS == "windows" {
		cmd = "echo build-ok"
	} else {
		cmd = "echo build-ok"
	}

	err := m.Build(context.Background(), cmd)
	if err != nil {
		t.Fatalf("expected build to succeed, got: %v", err)
	}
}

func TestBuildFailure(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	m := New(logger)

	var cmd string
	if runtime.GOOS == "windows" {
		cmd = "cmd /C exit 1"
	} else {
		cmd = "false"
	}

	err := m.Build(context.Background(), cmd)
	if err == nil {
		t.Fatal("expected build to fail")
	}
}

func TestBuildCancellation(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	m := New(logger)

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel immediately
	cancel()

	var cmd string
	if runtime.GOOS == "windows" {
		cmd = "timeout /T 10"
	} else {
		cmd = "sleep 10"
	}

	err := m.Build(ctx, cmd)
	if err == nil {
		t.Fatal("expected build to be cancelled")
	}
}

func TestStartAndStop(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	m := New(logger)

	var cmd string
	if runtime.GOOS == "windows" {
		cmd = "ping -n 60 127.0.0.1"
	} else {
		cmd = "sleep 60"
	}

	if err := m.Start(cmd); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}

	// Should be running
	time.Sleep(200 * time.Millisecond)
	if !m.IsRunning() {
		t.Error("expected process to be running")
	}

	// Stop
	if err := m.Stop(); err != nil {
		t.Fatalf("failed to stop process: %v", err)
	}

	// Should no longer be running
	time.Sleep(200 * time.Millisecond)
	if m.IsRunning() {
		t.Error("expected process to be stopped")
	}
}

func TestStopWhenNothingRunning(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	m := New(logger)

	// Should not error when nothing is running
	if err := m.Stop(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDoubleStop(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	m := New(logger)

	var cmd string
	if runtime.GOOS == "windows" {
		cmd = "ping -n 60 127.0.0.1"
	} else {
		cmd = "sleep 60"
	}

	if err := m.Start(cmd); err != nil {
		t.Fatalf("failed to start: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	// First stop
	if err := m.Stop(); err != nil {
		t.Fatalf("first stop error: %v", err)
	}

	// Second stop should be safe
	if err := m.Stop(); err != nil {
		t.Fatalf("second stop error: %v", err)
	}
}
