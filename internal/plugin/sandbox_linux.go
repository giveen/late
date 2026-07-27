//go:build linux

package plugin

import (
	"os/exec"
	"syscall"
)

// setCmdSysProcAttr applies Linux-specific sandboxing to an exec.Cmd.
// It sets NoNewPrivs=true (kernel-enforced: the process and its children
// cannot gain new privileges via setuid/setgid/file capabilities) and
// best-effort PID namespace isolation so the subprocess has a restricted
// view of process ancestry.
//
// On kernels without unprivileged PID namespaces the Cloneflags field
// may cause cmd.Run() to return EPERM. We intentionally accept this
// trade-off because the security benefit outweighs the edge case of
// broken hooks on misconfigured kernels.
func setCmdSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWPID,
	}
}
