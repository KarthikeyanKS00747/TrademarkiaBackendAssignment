package process

import (
	"fmt"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// setSysProcAttr sets platform-specific process attributes for Windows.
// CREATE_NEW_PROCESS_GROUP allows us to kill the entire process tree.
func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
}

// createJobObject creates a Windows Job Object configured to kill all
// processes in the job when the handle is closed.
func createJobObject() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("CreateJobObject: %w", err)
	}

	// Set the job to kill all processes when the job handle is closed
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}

	_, err = windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	)
	if err != nil {
		windows.CloseHandle(job)
		return 0, fmt.Errorf("SetInformationJobObject: %w", err)
	}

	return job, nil
}

// assignProcessToJob assigns a process to a Job Object.
func assignProcessToJob(job windows.Handle, pid int) error {
	handle, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(pid),
	)
	if err != nil {
		return fmt.Errorf("OpenProcess(%d): %w", pid, err)
	}
	defer windows.CloseHandle(handle)

	if err := windows.AssignProcessToJobObject(job, handle); err != nil {
		return fmt.Errorf("AssignProcessToJobObject: %w", err)
	}
	return nil
}

// terminateJob terminates all processes in the Job Object and closes the handle.
func terminateJob(job windows.Handle) error {
	err := windows.TerminateJobObject(job, 1)
	windows.CloseHandle(job)
	if err != nil {
		return fmt.Errorf("TerminateJobObject: %w", err)
	}
	return nil
}

// platformKillProcessTree kills a process tree on Windows using taskkill as fallback.
func platformKillProcessTree(pid int) error {
	return killProcessTreeWindows(pid)
}

// setupJobObject creates a Job Object and assigns the given process to it.
func (m *Manager) setupJobObject(pid int) error {
	job, err := createJobObject()
	if err != nil {
		return err
	}
	if err := assignProcessToJob(job, pid); err != nil {
		windows.CloseHandle(job)
		return err
	}
	m.jobHandle = uintptr(job)
	return nil
}

// terminateJobObject kills all processes in the Job Object.
func (m *Manager) terminateJobObject() {
	if m.jobHandle != 0 {
		_ = terminateJob(windows.Handle(m.jobHandle))
		m.jobHandle = 0
	}
}
