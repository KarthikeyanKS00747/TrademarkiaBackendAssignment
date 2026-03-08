//go:build !windows

package process

import (
	"os/exec"
	"syscall"
	"time"
)

// setSysProcAttr sets platform-specific process attributes for Unix.
// Setpgid creates a new process group so we can kill all children at once.
func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
}

// platformKillProcessTree kills a process tree on Unix using process groups.
func platformKillProcessTree(pid int) error {
	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		// If we can't get the group, just kill the process
		return syscall.Kill(pid, syscall.SIGKILL)
	}

	// First try SIGTERM to the group
	if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil {
		// If SIGTERM fails, try SIGKILL
		return syscall.Kill(-pgid, syscall.SIGKILL)
	}

	// Give processes time to handle SIGTERM
	time.Sleep(500 * time.Millisecond)

	// Then force kill any stragglers
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	return nil
}

// setupJobObject is a no-op on Unix (Job Objects are Windows-only).
func (m *Manager) setupJobObject(pid int) error {
	return nil
}

// terminateJobObject is a no-op on Unix.
func (m *Manager) terminateJobObject() {
}
