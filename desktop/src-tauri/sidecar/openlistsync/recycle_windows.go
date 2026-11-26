//go:build windows

package main

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"
)

const (
	foDelete          = 0x0003
	fofSilent         = 0x0004
	fofNoConfirmation = 0x0010
	fofAllowUndo      = 0x0040
	fofNoErrorUI      = 0x0400
)

type shFileOpStruct struct {
	hwnd                  uintptr
	wFunc                 uint32
	pFrom                 *uint16
	pTo                   *uint16
	fFlags                uint16
	fAnyOperationsAborted int32
	hNameMappings         uintptr
	lpszProgressTitle     *uint16
}

func init() { recycleFiles = recyclePathsWin }

// recyclePathsWin 用 SHFileOperationW(FOF_ALLOWUNDO) 把文件送进 Windows 回收站。
// 路径需与目标同盘有回收站；回收站被禁用/配额不足时返回错误，由调用方回退留档。
func recyclePathsWin(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	var buf []uint16
	for _, p := range paths {
		u, err := syscall.UTF16FromString(p)
		if err != nil {
			return err
		}
		buf = append(buf, u...) // 每个字符串自带结尾 NUL
	}
	buf = append(buf, 0) // 整个列表双 NUL 结尾

	op := shFileOpStruct{
		wFunc:  foDelete,
		pFrom:  &buf[0],
		fFlags: fofAllowUndo | fofNoConfirmation | fofSilent | fofNoErrorUI,
	}
	proc := syscall.NewLazyDLL("shell32.dll").NewProc("SHFileOperationW")
	r, _, _ := proc.Call(uintptr(unsafe.Pointer(&op)))
	if r != 0 {
		return fmt.Errorf("SHFileOperationW 返回 %d", r)
	}
	if op.fAnyOperationsAborted != 0 {
		return errors.New("回收站操作被系统中止")
	}
	return nil
}
