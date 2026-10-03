package jobs

import "testing"

// TestKillProcessGroupIgnoresNonPositivePID 锁住两个平台实现都有的 pid <= 0 守卫。
//
// 守卫不是装饰：Unix 侧是负数 PID = 整个进程组，pid = 1 时 syscall.Kill(-1, SIGKILL)
// 会波及本进程有权限信号的几乎所有进程；Windows 侧 taskkill /PID 0 会误杀别的进程。
// 若守卫被删掉，本测试会当场把测试进程一起带走——这正是它作为回归网的意义。
func TestKillProcessGroupIgnoresNonPositivePID(t *testing.T) {
	for _, pid := range []int{0, -1, -100} {
		killProcessGroup(pid)
	}
}
