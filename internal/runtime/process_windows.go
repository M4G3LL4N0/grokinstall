//go:build windows

package runtime

import "os/exec"

// isolate is a no-op on Windows.
//
// Process groups work differently there and CREATE_NEW_PROCESS_GROUP is not
// equivalent to the POSIX behaviour: it does not give a reliable way to kill the
// whole tree. The honest position is that descendant cleanup on Windows is not
// implemented, rather than pretending the Unix guarantee holds. Windows is not
// in the CI matrix, and the timeout still terminates the direct child.
func isolate(cmd *exec.Cmd) {}
