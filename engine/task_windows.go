//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}

func openPath(p string) {
	c := exec.Command("explorer.exe", p)
	hideWindow(c)
	_ = c.Start()
}
