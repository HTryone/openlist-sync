//go:build windows

package main

// 在进程内直接调用 Windows COM 的 IFileOpenDialog（多选文件夹）。
// 相比用 PowerShell 子进程：任务栏图标是本程序、DPI 由本进程(已声明 PMv2)决定、不会弹后台。

import (
	"runtime"
	"syscall"
	"unsafe"
)

type guid struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

var (
	ole32                    = syscall.NewLazyDLL("ole32.dll")
	procCoInitializeEx       = ole32.NewProc("CoInitializeEx")
	procCoCreateInstance     = ole32.NewProc("CoCreateInstance")
	procCoTaskMemFree        = ole32.NewProc("CoTaskMemFree")
	u32                      = syscall.NewLazyDLL("user32.dll")
	procGetForegroundWindow2 = u32.NewProc("GetForegroundWindow")
)

// CLSID_FileOpenDialog {DC1C5A9C-E88A-4dde-A5A1-60F82A20AEF7}
var clsidFileOpenDialog = guid{0xDC1C5A9C, 0xE88A, 0x4DDE, [8]byte{0xA5, 0xA1, 0x60, 0xF8, 0x2A, 0x20, 0xAE, 0xF7}}

// IID_IFileOpenDialog {d57c7288-d4ad-4768-be02-9d969532d960}
var iidIFileOpenDialog = guid{0xd57c7288, 0xd4AD, 0x4768, [8]byte{0xBE, 0x02, 0x9D, 0x96, 0x95, 0x32, 0xD9, 0x60}}

const (
	coinitApartmentThreaded = 0x2
	clsctxInprocServer      = 0x1
	fosPickFolders          = 0x20
	fosForceFileSystem      = 0x40
	fosAllowMultiSelect     = 0x200
	fosPathMustExist        = 0x800
	sigdnFileSysPath        = 0x80058000
)

// COM 接口方法索引
const (
	vtRelease      = 2
	vtShow         = 3  // IModalWindow::Show
	vtSetOptions   = 9  // IFileDialog::SetOptions
	vtGetOptions   = 10 // IFileDialog::GetOptions
	vtSetTitle     = 17 // IFileDialog::SetTitle
	vtGetResults   = 27 // IFileOpenDialog::GetResults
	arrGetCount    = 7  // IShellItemArray::GetCount
	arrGetItemAt   = 8  // IShellItemArray::GetItemAt
	itemGetDisplay = 5  // IShellItem::GetDisplayName
)

func comMethod(this uintptr, index int, args ...uintptr) uintptr {
	vtable := *(*uintptr)(unsafe.Pointer(this))
	fn := *(*uintptr)(unsafe.Pointer(vtable + uintptr(index)*unsafe.Sizeof(uintptr(0))))
	callArgs := append([]uintptr{this}, args...)
	r, _, _ := syscall.SyscallN(fn, callArgs...)
	return r
}

func release(p uintptr) {
	if p != 0 {
		comMethod(p, vtRelease)
	}
}

// comPickFolders 进程内弹多选文件夹对话框；取消返回空切片
func comPickFolders() ([]string, error) {
	// 必须在 STA 线程上操作
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	hr, _, _ := procCoInitializeEx.Call(0, uintptr(coinitApartmentThreaded))
	_ = hr // S_FALSE 已初始化也 OK
	defer ole32.NewProc("CoUninitialize").Call()

	var pDlg uintptr
	hr, _, _ = procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidFileOpenDialog)), 0,
		uintptr(clsctxInprocServer), uintptr(unsafe.Pointer(&iidIFileOpenDialog)),
		uintptr(unsafe.Pointer(&pDlg)))
	if int32(hr) != 0 || pDlg == 0 {
		return nil, syscall.Errno(hr)
	}
	defer release(pDlg)

	// 取现有 options 再叠加
	var opts uint32
	comMethod(pDlg, vtGetOptions, uintptr(unsafe.Pointer(&opts)))
	opts |= fosPickFolders | fosForceFileSystem | fosAllowMultiSelect | fosPathMUSTExist()
	comMethod(pDlg, vtSetOptions, uintptr(opts))

	title, _ := syscall.UTF16PtrFromString("选择要同步的文件夹（按住 Ctrl 可一次多选）")
	comMethod(pDlg, vtSetTitle, uintptr(unsafe.Pointer(title)))

	fg, _, _ := procGetForegroundWindow2.Call()
	hr = comMethod(pDlg, vtShow, fg)
	// 0=确定；0x800704C7=用户取消
	if uint32(hr) == 0x800704C7 {
		return nil, nil
	}
	if int32(hr) != 0 {
		return nil, syscall.Errno(hr)
	}

	var pArray uintptr
	hr = comMethod(pDlg, vtGetResults, uintptr(unsafe.Pointer(&pArray)))
	if int32(hr) != 0 || pArray == 0 {
		return nil, syscall.Errno(hr)
	}
	defer release(pArray)

	var count uint32
	comMethod(pArray, arrGetCount, uintptr(unsafe.Pointer(&count)))
	var out []string
	for i := uint32(0); i < count; i++ {
		var pItem uintptr
		if int32(comMethod(pArray, arrGetItemAt, uintptr(i), uintptr(unsafe.Pointer(&pItem)))) != 0 || pItem == 0 {
			continue
		}
		var pName uintptr
		if int32(comMethod(pItem, itemGetDisplay, uintptr(sigdnFileSysPath), uintptr(unsafe.Pointer(&pName)))) == 0 && pName != 0 {
			out = append(out, utf16PtrToString(pName))
			procCoTaskMemFree.Call(pName)
		}
		release(pItem)
	}
	return out, nil
}

// 小工具：常量名与上面常量保持一致的包装（避免笔误）
func fosPathMUSTExist() uint32 { return fosPathMustExist }

func utf16PtrToString(p uintptr) string {
	if p == 0 {
		return ""
	}
	var buf []uint16
	for i := 0; ; i++ {
		c := *(*uint16)(unsafe.Pointer(p + uintptr(i)*2))
		if c == 0 {
			break
		}
		buf = append(buf, c)
	}
	return syscall.UTF16ToString(buf)
}

// comPickerSelfCheck 无界面自检：创建对话框对象并释放，验证 COM 链路可用
func comPickerSelfCheck() error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr, _, _ := procCoInitializeEx.Call(0, uintptr(coinitApartmentThreaded))
	_ = hr
	defer ole32.NewProc("CoUninitialize").Call()
	var pDlg uintptr
	hr, _, _ = procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidFileOpenDialog)), 0, uintptr(clsctxInprocServer),
		uintptr(unsafe.Pointer(&iidIFileOpenDialog)), uintptr(unsafe.Pointer(&pDlg)))
	if int32(hr) != 0 || pDlg == 0 {
		return syscall.Errno(hr)
	}
	release(pDlg)
	return nil
}
