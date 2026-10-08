//go:build windows

package jobs

import (
	"os/exec"
	"strconv"
	"syscall"
)

// setProcessGroup 让子进程脱离本进程的控制台事件组，对标 Unix 侧把子进程放进独立进程组。
//
// 注意这里拿不到「杀整棵树」的能力：Windows 没有 Unix 那种「进程组 ID = 组长 PID」的
// 语义，CREATE_NEW_PROCESS_GROUP 只影响控制台 Ctrl+C/Ctrl+Break 的投递范围。
// 杀整棵树由 killProcessGroup 用 taskkill /T 完成。
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// killProcessGroup 杀掉的是一棵进程树，而不只是直接子进程（对应「杀整棵树」）。
//
// 用 taskkill /T /F：/T 按父子关系连根杀掉子进程派生的进程，/F 强制结束。
// 不用 Job Object（那会连带要求 golang.org/x/sys/windows 这个新依赖），也不用
// cmd.Process.Kill()——后者只杀直接子进程，正好是要避免的形态。
func killProcessGroup(pid int) {
	// pid <= 0 说明进程还没起来（启动失败路径），没有可杀的树。
	if pid <= 0 {
		return
	}
	_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run()
}
