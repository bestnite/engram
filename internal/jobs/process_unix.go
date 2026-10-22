//go:build unix

package jobs

import (
	"os/exec"
	"syscall"
)

// setProcessGroup makes the subprocess the leader of its own process group.
//
// Making it its own group is the precondition for "killing the whole tree": the process group ID equals the leader's PID, so killProcessGroup can
// cover the whole group at once with a negative PID. For why the whole tree must be killed, see the note on runProcess.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup sends SIGKILL to the entire process group the subprocess belongs to.
//
// Setpgid makes the subprocess PID the group leader, so a negative PID means the whole group; killing only the direct child would leave the processes it spawned
// (for example a background training process under a shell) behind, still burning CPU and making the job "look finished while it is still running"
// — which is exactly why "kill the whole process group" is required.
func killProcessGroup(pid int) {
	// pid <= 0 means the process has not started (the start-failure path); a negative PID would hit the current process group, so it must be blocked.
	if pid <= 0 {
		return
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}
