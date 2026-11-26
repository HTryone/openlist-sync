//go:build windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

// sendGracefulTerminate 优雅终止：Windows 下用 GenerateConsoleCtrlEvent(CTRL_BREAK)，
// rclone mount 捕获后会安全卸载盘符再退出；失败再回退到 Kill。
func sendGracefulTerminate(p *os.Process) {
	if p == nil {
		return
	}
	// 只有在创建子进程时设置了 CREATE_NEW_PROCESS_GROUP 才能投递 CTRL_BREAK
	send := syscall.NewLazyDLL("kernel32.dll").NewProc("GenerateConsoleCtrlEvent")
	if r, _, _ := send.Call(1, uintptr(p.Pid)); r != 0 { // CTRL_BREAK_EVENT = 1
		return
	}
	_ = p.Kill()
	_ = exec.Command("cmd", "/c", "taskkill /PID "+itoa(p.Pid)+" /F /T").Run()
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}
