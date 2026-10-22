//go:build windows

package jobs

import (
	"os/exec"
	"strconv"
	"syscall"
)

// setProcessGroup detaches the subprocess from this process's console event group, mirroring the Unix side putting the subprocess in its own process group.
//
// Note that this does not give us "kill the whole tree": Windows lacks Unix's "process group ID = leader PID"
// semantics, and CREATE_NEW_PROCESS_GROUP only affects the delivery scope of console Ctrl+C/Ctrl+Break.
// Killing the whole tree is done by killProcessGroup with taskkill /T.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// killProcessGroup kills a whole process tree, not just the direct child (corresponding to "kill the whole tree").
//
// It uses taskkill /T /F: /T kills down the parent-child chain, including processes the child spawned, and /F forces termination.
// It does not use a Job Object (which would bring in a new dependency, golang.org/x/sys/windows), nor
// cmd.Process.Kill() — the latter kills only the direct child, exactly the shape to avoid.
func killProcessGroup(pid int) {
	// pid <= 0 means the process has not started (the start-failure path), so there is no tree to kill.
	if pid <= 0 {
		return
	}
	_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run()
}
