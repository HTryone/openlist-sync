//go:build windows

package main

import "syscall"

// 声明 Per-Monitor V2 DPI 感知，避免系统原生对话框被位图拉伸而发虚。
// 必须在创建任何窗口前调用（main 里尽早调用）。
func initDPIAwareness() {
	dll, err := syscall.LoadDLL("user32.dll")
	if err != nil {
		return
	}
	// SetProcessDpiAwarenessContext(-4 = PER_MONITOR_AWARE_V2)，Win10 1703+
	if p, e := dll.FindProc("SetProcessDpiAwarenessContext"); e == nil {
		if r, _, _ := p.Call(uintptr(0xFFFFFFFFFFFFFFFC)); r != 0 {
			return
		}
	}
	// 退回 SetProcessDPIAware()
	if p, e := dll.FindProc("SetProcessDPIAware"); e == nil {
		p.Call()
	}
}
