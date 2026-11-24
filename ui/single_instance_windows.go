//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

var (
	kernel32Single  = syscall.NewLazyDLL("kernel32.dll")
	procCreateMutex = kernel32Single.NewProc("CreateMutexW")
)

var singleMutex syscall.Handle

// acquireSingleInstance 尝试创建命名互斥量；若已存在（已有实例在跑）返回 false。
// 名称用 Local\ 前缀 = 按登录会话隔离，不同用户各自一份。
func acquireSingleInstance(name string) bool {
	p, _ := syscall.UTF16PtrFromString(name)
	h, _, err := procCreateMutex.Call(0, 0, uintptr(unsafe.Pointer(p)))
	if h == 0 {
		return true // 创建失败不阻塞启动
	}
	singleMutex = syscall.Handle(h)
	if errno, ok := err.(syscall.Errno); ok && errno == 183 { // ERROR_ALREADY_EXISTS
		return false
	}
	return true
}
