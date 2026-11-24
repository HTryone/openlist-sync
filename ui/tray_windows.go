//go:build windows

package main

import (
	"net/http"
	"os"
	"syscall"
	"time"
	"unsafe"
)

var useTray = true

func utf16ptr(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

// 托盘回调上下文
var (
	trayApp  *App
	trayAddr string
)

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	user32           = syscall.NewLazyDLL("user32.dll")
	shell32          = syscall.NewLazyDLL("shell32.dll")
	pGetModuleHandle = kernel32.NewProc("GetModuleHandleW")
	pRegisterClass   = user32.NewProc("RegisterClassExW")
	pCreateWindowEx  = user32.NewProc("CreateWindowExW")
	pDefWindowProc   = user32.NewProc("DefWindowProcW")
	pDestroyWindow   = user32.NewProc("DestroyWindow")
	pGetMessage      = user32.NewProc("GetMessageW")
	pTranslateMsg    = user32.NewProc("TranslateMessage")
	pDispatchMsg     = user32.NewProc("DispatchMessageW")
	pPostQuitMessage = user32.NewProc("PostQuitMessage")
	pCreatePopupMenu = user32.NewProc("CreatePopupMenu")
	pAppendMenu      = user32.NewProc("AppendMenuW")
	pTrackPopupMenu  = user32.NewProc("TrackPopupMenu")
	pSetForeground   = user32.NewProc("SetForegroundWindow")
	pGetCursorPos    = user32.NewProc("GetCursorPos")
	pLoadIcon        = user32.NewProc("LoadIconW")
	pPostMessage     = user32.NewProc("PostMessageW")
	pShellNotifyIcon = shell32.NewProc("Shell_NotifyIconW")
)

type wndClassEx struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     syscall.Handle
	hIcon         syscall.Handle
	hCursor       syscall.Handle
	hbrBackground syscall.Handle
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       syscall.Handle
}

type msgStruct struct {
	hwnd    syscall.Handle
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      pointStruct
}

type pointStruct struct {
	x int32
	y int32
}

type notifyIconData struct {
	cbSize            uint32
	hWnd              syscall.Handle
	uID               uint32
	uFlags            uint32
	uCallbackMessage  uint32
	hIcon             syscall.Handle
	szTip             [128]uint16
	dwState           uint32
	dwStateMask       uint32
	szInfo            [256]uint16
	uTimeoutOrVersion uint32
	szInfoTitle       [64]uint16
	dwInfoFlags       uint32
	guidItem          [16]byte
	hBalloonIcon      syscall.Handle
}

const (
	wmDestroy    = 0x0002
	wmCommand    = 0x0111
	trayCallback = 0x8001 // WM_APP+1
	nimAdd       = 0
	nimDelete    = 2
	nifMessage   = 0x01
	nifIcon      = 0x02
	nifTip       = 0x04
	wmLButtonDbl = 0x0203
	wmRButtonUp  = 0x0205
	mfString     = 0x0000
	tpmRightBtn  = 0x0002
	idiApp       = 32512
)

func utf16(s string) []uint16 {
	p, _ := syscall.UTF16FromString(s)
	return p
}

func loword(v uintptr) uint16 { return uint16(v & 0xffff) }

func trayWndProc(hwnd syscall.Handle, msg uint32, wparam, lparam uintptr) uintptr {
	switch msg {
	case trayCallback:
		switch uint32(lparam) {
		case wmLButtonDbl:
			openPath("http://" + trayAddr + "/")
		case wmRButtonUp:
			showTrayMenu(hwnd)
		}
	case wmCommand:
		switch loword(wparam) {
		case 1:
			openPath("http://" + trayAddr + "/")
		case 2:
			go trayApp.runAll()
		case 3:
			trayDelete(hwnd)
			// 退出前：先卸载网盘挂载（否则 rclone mount 子进程会残留成死链）
			trayApp.doUnmount()
			pPostQuitMessage.Call(0)
		}
	case wmDestroy:
		trayApp.doUnmount()
		pPostQuitMessage.Call(0)
	}
	r, _, _ := pDefWindowProc.Call(uintptr(hwnd), uintptr(msg), wparam, lparam)
	return r
}

func showTrayMenu(hwnd syscall.Handle) {
	menu, _, _ := pCreatePopupMenu.Call()
	pAppendMenu.Call(menu, mfString, 1, uintptr(unsafe.Pointer(utf16ptr("打开面板"))))
	pAppendMenu.Call(menu, mfString, 2, uintptr(unsafe.Pointer(utf16ptr("立即同步"))))
	pAppendMenu.Call(menu, 0x00000800, 0, 0) // MF_SEPARATOR
	pAppendMenu.Call(menu, mfString, 3, uintptr(unsafe.Pointer(utf16ptr("退出"))))
	var pt pointStruct
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	pSetForeground.Call(uintptr(hwnd))
	pTrackPopupMenu.Call(menu, tpmRightBtn, uintptr(pt.x), uintptr(pt.y), 0, uintptr(hwnd), 0)
	pPostMessage.Call(uintptr(hwnd), 0, 0, 0) // WM_NULL 让菜单正常关闭
}

func trayDelete(hwnd syscall.Handle) {
	var nid notifyIconData
	nid.cbSize = uint32(unsafe.Sizeof(nid))
	nid.hWnd = hwnd
	nid.uID = 1
	pShellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
}

// trayRun 启动托盘 + HTTP 服务，进入消息循环；成功则阻塞直到退出并返回 true
func trayRun(app *App, addr string, mux *http.ServeMux) bool {
	trayApp = app
	trayAddr = addr

	hInst, _, _ := pGetModuleHandle.Call(0)
	className := utf16ptr("OpenListSyncTray")

	var wc wndClassEx
	wc.cbSize = uint32(unsafe.Sizeof(wc))
	wc.lpfnWndProc = syscall.NewCallback(trayWndProc)
	wc.hInstance = syscall.Handle(hInst)
	wc.lpszClassName = className
	if r, _, _ := pRegisterClass.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return false
	}
	// 消息窗口（HWND_MESSAGE = -3，隐藏）
	hwnd, _, _ := pCreateWindowEx.Call(0, uintptr(unsafe.Pointer(className)), 0, 0, 0, 0, 0, 0, uintptr(^uintptr(2)), 0, hInst, 0)
	if hwnd == 0 {
		return false
	}

	hIcon, _, _ := pLoadIcon.Call(0, idiApp)

	var nid notifyIconData
	nid.cbSize = uint32(unsafe.Sizeof(nid))
	nid.hWnd = syscall.Handle(hwnd)
	nid.uID = 1
	nid.uFlags = nifMessage | nifIcon | nifTip
	nid.uCallbackMessage = trayCallback
	nid.hIcon = syscall.Handle(hIcon)
	tip := utf16("OpenList 同步（双击打开面板）")
	copy(nid.szTip[:], tip)
	if r, _, _ := pShellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(&nid))); r == 0 {
		return false
	}

	// HTTP 服务放到后台协程
	go func() {
		_ = http.ListenAndServe(addr, mux)
	}()
	// 打开面板
	go func() {
		time.Sleep(900)
		openPath("http://" + addr + "/")
	}()

	// 消息循环
	var m msgStruct
	for {
		r, _, _ := pGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		pTranslateMsg.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMsg.Call(uintptr(unsafe.Pointer(&m)))
	}
	trayDelete(syscall.Handle(hwnd))
	os.Exit(0)
	return true
}
