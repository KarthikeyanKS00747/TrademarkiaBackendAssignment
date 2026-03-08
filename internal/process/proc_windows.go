package process

import (
	"os/exec"
	"syscall"
)

// setSysProcAttr sets platform-specific process attributes for Windows.
// CREATE_NEW_PROCESS_GROUP allows us to kill the entire process tree.
func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
}

// platformKillProcessTree kills a process tree on Windows using taskkill.
func platformKillProcessTree(pid int) error {
	return killProcessTreeWindows(pid)
}
