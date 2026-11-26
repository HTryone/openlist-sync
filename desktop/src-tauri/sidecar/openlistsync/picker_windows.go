//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// 现代 Windows 文件夹多选对话框（IFileOpenDialog + FOS_PICKFOLDERS + FOS_ALLOWMULTISELECT）。
// 一个窗口里按住 Ctrl 可一次选中多个文件夹，替代老式 FolderBrowserDialog 的“一个个选+询问”循环。
const pickerPS = `$ErrorActionPreference = 'Stop'
# 关键：在任何窗口创建前声明 Per-Monitor V2 DPI 感知，避免 200% 缩放下对话框被位图拉伸发虚
Add-Type @'
using System;
using System.Runtime.InteropServices;
public static class DpiFix {
    [DllImport("user32.dll")] static extern bool SetProcessDpiAwarenessContext(IntPtr ctx);
    [DllImport("user32.dll")] static extern bool SetProcessDPIAware();
    [DllImport("shcore.dll")] static extern int SetProcessDpiAwareness(int v);
    public static void Apply() {
        try { if (SetProcessDpiAwarenessContext(new IntPtr(-4))) return; } catch {}
        try { if (SetProcessDpiAwareness(2) == 0) return; } catch {}
        try { SetProcessDPIAware(); } catch {}
    }
}
'@
[DpiFix]::Apply()
$code = @'
using System;
using System.Collections.Generic;
using System.Runtime.InteropServices;

public static class FolderPicker
{
    [ComImport, Guid("DC1C5A9C-E88A-4dde-A5A1-60F82A20AEF7")]
    private class FileOpenDialogRCW { }

    [ComImport, Guid("d57c7288-d4ad-4768-be02-9d969532d960"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
    private interface IFileOpenDialog
    {
        [PreserveSig] int Show(IntPtr parent);
        void SetFileTypes(uint cFileTypes, IntPtr rgFilterSpec);
        void SetFileTypeIndex(uint iFileType);
        void GetFileTypeIndex(out uint piFileType);
        void Advise(IntPtr pfde, out uint pdwCookie);
        void Unadvise(uint dwCookie);
        void SetOptions(uint fos);
        void GetOptions(out uint pfos);
        void SetDefaultFolder(IntPtr psi);
        void SetFolder(IntPtr psi);
        void GetFolder(out IntPtr ppsi);
        void GetCurrentSelection(out IntPtr ppsi);
        void SetFileName([MarshalAs(UnmanagedType.LPWStr)] string pszName);
        void GetFileName([MarshalAs(UnmanagedType.LPWStr)] out string pszName);
        void SetTitle([MarshalAs(UnmanagedType.LPWStr)] string pszTitle);
        void SetOkButtonLabel([MarshalAs(UnmanagedType.LPWStr)] string pszText);
        void SetFileNameLabel([MarshalAs(UnmanagedType.LPWStr)] string pszLabel);
        void GetResult(out IntPtr ppsi);
        void AddPlace(IntPtr psi, uint fdap);
        void SetDefaultExtension([MarshalAs(UnmanagedType.LPWStr)] string pszDefaultExtension);
        void Close(int hr);
        void SetClientGuid(ref Guid guid);
        void ClearClientData();
        void SetFilter(IntPtr pFilter);
        void GetResults(out IntPtr ppenum);
        void GetSelectedItems(out IntPtr ppsai);
    }

    [ComImport, Guid("43826d1e-e718-42ee-bc55-a1e261c37bfe"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
    private interface IShellItem
    {
        void BindToHandler(IntPtr pbc, ref Guid bhid, ref Guid riid, out IntPtr ppv);
        void GetParent(out IntPtr ppsi);
        void GetDisplayName(uint sigdnName, out IntPtr ppszName);
        void GetAttributes(uint sfgaoMask, out uint psfgaoAttribs);
        void Compare(IntPtr psi, uint hint, out int piOrder);
    }

    [ComImport, Guid("b63ea76d-1f85-456f-a19c-48159efa858b"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
    private interface IShellItemArray
    {
        void BindToHandler(IntPtr pbc, ref Guid bhid, ref Guid riid, out IntPtr ppv);
        void GetPropertyStore(uint flags, ref Guid riid, out IntPtr ppv);
        void GetPropertyDescriptionList(IntPtr keyType, ref Guid riid, out IntPtr ppv);
        void GetAttributes(uint attribFlags, uint sfgaoMask, out uint psfgaoAttribs);
        void GetCount(out uint pdwNumItems);
        void GetItemAt(uint dwIndex, out IntPtr ppsi);
        void EnumItems(out IntPtr ppenumShellItems);
    }

    const uint FOS_PICKFOLDERS = 0x20;
    const uint FOS_FORCEFILESYSTEM = 0x40;
    const uint FOS_ALLOWMULTISELECT = 0x200;
    const uint FOS_PATHMUSTEXIST = 0x800;
    const uint SIGDN_FILESYSPATH = 0x80058000;

    [DllImport("user32.dll")] static extern IntPtr GetForegroundWindow();
    [DllImport("user32.dll")] static extern bool SetForegroundWindow(IntPtr hWnd);

    public static string CompileCheck()
    {
        var dlg = (IFileOpenDialog)new FileOpenDialogRCW();
        uint opts; dlg.GetOptions(out opts);
        dlg.SetOptions(opts | FOS_PICKFOLDERS | FOS_FORCEFILESYSTEM | FOS_ALLOWMULTISELECT | FOS_PATHMUSTEXIST);
        return "OK";
    }

    public static string[] Pick()
    {
        var dlg = (IFileOpenDialog)new FileOpenDialogRCW();
        uint opts; dlg.GetOptions(out opts);
        dlg.SetOptions(opts | FOS_PICKFOLDERS | FOS_FORCEFILESYSTEM | FOS_ALLOWMULTISELECT | FOS_PATHMUSTEXIST);
        dlg.SetTitle("选择要同步的文件夹（按住 Ctrl 可一次多选）");
        // 用当前前台窗口(浏览器)作为 owner，并前置，避免对话框弹到后台
        IntPtr owner = GetForegroundWindow();
        if (owner != IntPtr.Zero) SetForegroundWindow(owner);
        int hr = dlg.Show(owner);
        if (hr != 0) return new string[0];
        IntPtr ppenum;
        dlg.GetResults(out ppenum);
        var arr = (IShellItemArray)Marshal.GetObjectForIUnknown(ppenum);
        uint count; arr.GetCount(out count);
        var list = new List<string>();
        for (uint i = 0; i < count; i++)
        {
            IntPtr psiPtr;
            arr.GetItemAt(i, out psiPtr);
            var item = (IShellItem)Marshal.GetObjectForIUnknown(psiPtr);
            IntPtr namePtr;
            item.GetDisplayName(SIGDN_FILESYSPATH, out namePtr);
            string s = Marshal.PtrToStringUni(namePtr);
            if (!string.IsNullOrEmpty(s)) list.Add(s);
            Marshal.Release(psiPtr);
        }
        return list.ToArray();
    }
}
'@
Add-Type -TypeDefinition $code -Language CSharp | Out-Null
if ($env:OPENLIST_PICK_MODE -eq 'compilecheck') {
  try { $r = [FolderPicker]::CompileCheck(); Write-Output ('COMPILE:' + $r) }
  catch { Write-Output ('COMPILEERR:' + $_.Exception.Message) }
  exit 0
}
try { $paths = [FolderPicker]::Pick() }
catch { Write-Output ('PICKERR:' + $_.Exception.Message); exit 0 }
foreach ($p in $paths) { Write-Output ('PICKED:' + $p) }
`

// runPicker 写出并执行选择脚本；mode=="compilecheck" 只做编译+COM 自检不弹窗
func runPicker(mode string) (string, error) {
	ps := filepath.Join(os.TempDir(), "openlist-pickdirs.ps1")
	if err := os.WriteFile(ps, []byte(pickerPS), 0o644); err != nil {
		return "", err
	}
	var lastErr error
	for _, exe := range []string{"pwsh", "powershell"} {
		if _, err := exec.LookPath(exe); err != nil {
			continue
		}
		cmd := exec.Command(exe, "-STA", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", ps)
		cmd.Env = append(os.Environ(), "OPENLIST_PICK_MODE="+mode)
		// 只隐藏控制台窗口(CREATE_NO_WINDOW)，不要 SW_HIDE——否则把它弹出的文件夹对话框也一起藏了
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000}
		out, err := cmd.CombinedOutput()
		if err != nil {
			lastErr = fmt.Errorf("%v: %s", err, string(out))
			continue
		}
		return string(out), nil
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", fmt.Errorf("找不到 PowerShell")
}

func pickDirectories() ([]string, error) {
	// 首选：进程内 COM 对话框（任务栏是本程序图标、DPI 本进程负责、弹前台）
	if paths, err := comPickFolders(); err == nil {
		return paths, nil
	}
	// 兜底：PowerShell 子进程
	out, err := runPicker("")
	if err != nil && strings.TrimSpace(out) == "" {
		return nil, err
	}
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		switch {
		case strings.HasPrefix(line, "PICKED:"):
			if p := strings.TrimSpace(strings.TrimPrefix(line, "PICKED:")); p != "" {
				paths = append(paths, p)
			}
		case strings.HasPrefix(line, "PICKERR:"):
			return nil, fmt.Errorf("%s", strings.TrimPrefix(line, "PICKERR:"))
		case strings.HasPrefix(line, "COMPILEERR:"):
			return nil, fmt.Errorf("选择器编译失败: %s", strings.TrimPrefix(line, "COMPILEERR:"))
		}
	}
	return paths, nil
}
