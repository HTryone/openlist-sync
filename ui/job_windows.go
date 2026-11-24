//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

// 把当前进程加入 Windows Job Object（KILL_ON_JOB_CLOSE）：
// 主程序无论正常退出还是被强杀，它启动的所有子进程（rclone mount 等）都会被系统一起终止，
// 避免托盘退出后残留挂载死链。
var jobHandle uintptr

// JOBOBJECT_BASIC_LIMIT_INFORMATION (x64) 的 LimitFlags 偏移：
//
//	PerProcessUserTimeLimit 8 + PerJobUserTimeLimit 8 + MinWorkingSetSize 8
//	+ MaxWorkingSetSize 8 + ActiveProcessLimit 4(+4 pad) + Affinity 8
//	+ PriorityClass 4(+4 pad) = 56 → LimitFlags 在偏移 56
const jobLimitFlagsOffset = 56
const jobObjectExtendedLimitInfoClass = 9
const jobObjectLimitKillOnJobClose = 0x2000

func initJobObject() {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	pCreateJobObject := kernel32.NewProc("CreateJobObjectW")
	pSetInfoJobObject := kernel32.NewProc("SetInformationJobObject")
	pAssignJobObject := kernel32.NewProc("AssignProcessToJobObject")
	pGetCurrentProcess := kernel32.NewProc("GetCurrentProcess")

	h, _, _ := pCreateJobObject.Call(0, 0)
	if h == 0 {
		return
	}
	jobHandle = h

	// JOBOBJECT_EXTENDED_LIMIT_INFORMATION 总长 144（x64），只写 Basic 部分的 LimitFlags
	buf := make([]byte, 144)
	// little-endian: 0x2000 → buf[56]=0x00, buf[57]=0x20
	buf[jobLimitFlagsOffset] = 0x00
	buf[jobLimitFlagsOffset+1] = 0x20

	r, _, _ := pSetInfoJobObject.Call(h, uintptr(jobObjectExtendedLimitInfoClass), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if r == 0 {
		return
	}
	cur, _, _ := pGetCurrentProcess.Call()
	if rr, _, _ := pAssignJobObject.Call(cur, h); rr == 0 {
		return
	}
	// 给一点时间让句柄关联稳定（否则极快退出时可能丢失）
	_ = jobHandle
}
