//go:build !windows

package runtime

import (
	"os/exec"
	"syscall"
)

// isolate puts the capability in its own process group and arranges for the
// whole group to be killed when the context is cancelled.
//
// This exists because exec.CommandContext's default behaviour kills only the
// direct child. A capability that backgrounds work — `(sleep 3; write marker) &`
// — therefore survived its own timeout and kept running with the runtime's
// privileges. That is a containment failure, not a test artefact: the timeout
// promised the caller that execution had stopped, and it had not.
//
// WaitDelay bounds how long Run waits on the pipes after a kill. It does not
// kill descendants, so it is not a substitute for this.
func isolate(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// Negative pid targets the process group, so descendants die with the
		// parent. If the group is already gone ESRCH is the correct answer and
		// not an error worth surfacing to the capability.
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			if err == syscall.ESRCH {
				return nil
			}
			return err
		}
		return nil
	}
}
