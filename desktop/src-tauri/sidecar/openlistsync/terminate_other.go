//go:build !windows

package main

import "os"

func sendGracefulTerminate(p *os.Process) {
	if p != nil {
		_ = p.Kill()
	}
}
