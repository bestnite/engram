//go:build unix

package jobs

import (
	"os/exec"
	"syscall"
)

// setProcessGroup 让子进程成为一个独立进程组的组长。
//
// 单独成组是「杀整棵树」的前提：进程组 ID 等于组长 PID，于是 killProcessGroup 可以用
// 负 PID 一次覆盖整组。要杀整棵树的原因见 runProcess 的说明（M9-1）。
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup 向子进程所在的整个进程组发 SIGKILL。
//
// Setpgid 让子进程 PID 成为组长，故负 PID 即整组；只杀直接子进程会留下它派生的进程
// （例如 shell 下的后台训练进程），它们继续占 CPU 并让作业「看似结束实则仍在跑」
// ——这正是 M9-1 要求「杀整个进程组」的原因。
func killProcessGroup(pid int) {
	// pid <= 0 说明进程还没起来（启动失败路径），负 PID 会误伤当前进程组，必须挡住。
	if pid <= 0 {
		return
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}
