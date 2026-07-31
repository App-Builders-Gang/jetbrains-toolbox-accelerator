//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

const (
	detachedProcess        = 0x00000008
	createNewProcessGroup  = 0x00000200
)

// detach makes the child survive this process exiting and keeps it off the
// console, so `jtaccel install` can return while the proxy keeps running.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: detachedProcess | createNewProcessGroup,
		HideWindow:    true,
	}
}

// hideConsole releases any inherited console so daemon mode leaves no window
// behind. Windows has no equivalent of socket activation, so the proxy stays
// resident -- this at least keeps it invisible.
func hideConsole() {
	_, _, _ = syscall.NewLazyDLL("kernel32.dll").NewProc("FreeConsole").Call()
}
