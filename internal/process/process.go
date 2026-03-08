package process

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Manager handles building and running processes, ensuring proper lifecycle.
type Manager struct {
	logger    *slog.Logger
	mu        sync.Mutex
	running   *exec.Cmd
	cancel    context.CancelFunc // cancel for the running server process
	done      chan struct{}       // closed when the running process exits
	jobHandle uintptr            // Windows Job Object handle (0 on Unix)
}

// New returns a new process Manager.
func New(logger *slog.Logger) *Manager {
	return &Manager{
		logger: logger,
	}
}

// Build executes the build command. Returns nil on success.
// The context allows cancellation if a new change arrives during build.
func (m *Manager) Build(ctx context.Context, buildCmd string) error {
	m.logger.Info("building project", "cmd", buildCmd)
	start := time.Now()

	cmd := createCommand(ctx, buildCmd)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("build cancelled: %w", ctx.Err())
		}
		return fmt.Errorf("build failed: %w", err)
	}

	m.logger.Info("build successful", "duration", time.Since(start).Round(time.Millisecond))
	return nil
}

// Start launches the exec command as a background process and streams its output.
// It returns immediately after the process is started.
func (m *Manager) Start(execCmd string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel

	cmd := createCommand(ctx, execCmd)

	// Stream stdout and stderr in real-time (no buffering)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("failed to get stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("failed to get stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("failed to start server: %w", err)
	}

	// On Windows, assign the process to a Job Object so we can kill
	// the entire process tree (including grandchildren) reliably.
	if runtime.GOOS == "windows" {
		if err := m.setupJobObject(cmd.Process.Pid); err != nil {
			m.logger.Warn("failed to set up job object, falling back to taskkill", "error", err)
		}
	}

	m.running = cmd
	done := make(chan struct{})
	m.done = done
	m.logger.Info("server started", "pid", cmd.Process.Pid, "cmd", execCmd)

	// Stream output in real-time
	go streamOutput(stdout, os.Stdout, m.logger)
	go streamOutput(stderr, os.Stderr, m.logger)

	// Wait for process to complete in background
	go func() {
		err := cmd.Wait()
		m.mu.Lock()
		if m.running == cmd {
			m.running = nil
		}
		m.mu.Unlock()
		close(done)

		if err != nil && ctx.Err() == nil {
			m.logger.Warn("server process exited with error", "error", err)
		} else if ctx.Err() == nil {
			m.logger.Info("server process exited normally")
		}
	}()

	return nil
}

// Stop terminates the currently running server process and all its children.
func (m *Manager) Stop() error {
	m.mu.Lock()
	cmd := m.running
	cancel := m.cancel
	done := m.done
	m.running = nil
	m.cancel = nil
	m.done = nil
	m.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		return nil
	}

	m.logger.Info("stopping server", "pid", cmd.Process.Pid)

	// Cancel the context first
	if cancel != nil {
		cancel()
	}

	// Use Job Object on Windows if available, otherwise fall back to process tree kill
	if runtime.GOOS == "windows" && m.jobHandle != 0 {
		m.terminateJobObject()
	} else if err := killProcessTree(cmd.Process.Pid); err != nil {
		m.logger.Warn("failed to kill process tree, attempting force kill", "error", err)
		_ = cmd.Process.Kill()
	}

	// Wait for the process to fully terminate using the done channel from Start()
	if done != nil {
		select {
		case <-done:
			m.logger.Debug("server stopped successfully")
		case <-time.After(5 * time.Second):
			m.logger.Warn("server did not stop in time, force killing")
			_ = cmd.Process.Kill()
			<-done
		}
	}

	return nil
}

// IsRunning returns true if a server process is currently running.
func (m *Manager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running != nil
}

// createCommand creates an exec.Cmd with proper setup for the OS.
func createCommand(ctx context.Context, command string) *exec.Cmd {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", command)
	}

	// Set process group so we can kill all children
	setSysProcAttr(cmd)

	return cmd
}

// streamOutput copies from reader to writer in real-time.
func streamOutput(r io.ReadCloser, w io.Writer, logger *slog.Logger) {
	buf := make([]byte, 1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				logger.Debug("error writing output", "error", writeErr)
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// killProcessTree kills a process and all its children.
func killProcessTree(pid int) error {
	return platformKillProcessTree(pid)
}

func killProcessTreeWindows(pid int) error {
	// On Windows, use taskkill /T to kill the process tree
	cmd := exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprintf("%d", pid))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("taskkill failed: %w, output: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
