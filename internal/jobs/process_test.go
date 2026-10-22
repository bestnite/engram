package jobs

import "testing"

// TestKillProcessGroupIgnoresNonPositivePID pins down the pid <= 0 guard both platform implementations have.
//
// The guard is not decoration: on the Unix side a negative PID means the whole process group, and with pid = 1 syscall.Kill(-1, SIGKILL)
// would hit almost every process this process has permission to signal; on the Windows side taskkill /PID 0 kills unrelated processes.
// If the guard were removed, this test would take the test process down with it on the spot — which is exactly its value as a regression net.
func TestKillProcessGroupIgnoresNonPositivePID(t *testing.T) {
	for _, pid := range []int{0, -1, -100} {
		killProcessGroup(pid)
	}
}
