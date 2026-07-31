//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// detach puts the child in its own session so it survives the parent exiting.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// hideConsole is a no-op outside Windows; launchd and systemd already run the
// daemon without a controlling terminal.
func hideConsole() {}
