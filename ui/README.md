# OpenList 网盘同步（Windows 桌面端）

把多个本地文件夹像"上传网盘"一样，定期**双向同步**到 OpenList（WebDAV）。
本地始终保有一份完整副本，Agent/程序读写全走本地；后台按间隔自动同步；删除归档到网盘。

- **双击即用**：`OpenListSync.exe` → 右下角托盘图标 + 自动打开配置面板 `http://127.0.0.1:5399/`
- **无黑窗、单实例**：重复双击只会打开已有面板，不会开第二个进程
- **关闭**：托盘图标右键 →「退出」
- **自包含**：自带 `rclone\rclone.exe`，无需另装 rclone

## 分发与使用

分发包就是 `dist\`（`OpenListSync.exe` + `rclone\rclone.exe`），解压到任意**可写目录**（如桌面 / `D:\OpenListSync`）
双击 exe 即用。**不要放在 `C:\Program Files\` 等只读目录**（配置写不进去）。

首次使用：
1. 填「网盘地址 / 账号 / 密码」，**保存设置** → **测试连接**
2. 「网盘目录」选一个网盘上的上级目录（如 `/ty2`），系统会**自动在其下建 `agents-sy`**，所有同步文件夹归入其下
3. 「+ 添加一行 / 多选添加」用**系统文件夹对话框**选本地文件夹
   → 名字自动取文件夹名，网盘目标自动 = `agents-sy/<文件夹名>`，并**自动在网盘建好目录**
4. 左侧「自动同步」设间隔与开关 → 保存，即按间隔自动同步（首次自动建基线）

## 目录对应

```
本地 D:\项目\甲方   →   网盘 openlist:/ty2/agents-sy/甲方
```

- 一个本地文件夹 = `agents-sy/` 下一个同名子目录
- 本地删除 → 远端删除，归档到 `openlist:/ty2/agents-sy/.trash/<文件夹名>/`（各自独立，不会被二次清理）
- 本地不保留归档（本地删除进系统回收站）

## `.nosy` 过滤（可选，放本地文件夹根目录）

gitignore 语法，排除不想同步的内容；面板内点「过滤规则 .nosy」可直接编辑：

```
node_modules/
build/
dist/
*.tmp
.env
# 排除某目录，但保留其中一个文件
docs/*
!docs/README.md
```

`.nosy` 本身不参与同步。

## 已知限制

- 比较方式为 **size-only**：服务端（如天翼云盘）不暴露哈希、修改时间不可靠。
  因此**"大小不变、仅改内容"的修改检测不到** —— 请确保编辑后文件大小发生变化。
- 依赖 Windows（托盘、系统文件夹对话框、DPI 感知）。Linux 服务器部署见仓库 `deploy/`。

## 配置文件在哪

全部在 **exe 同目录**（首次运行自动生成）：

```
OpenListSync.exe
rclone\rclone.exe          自带同步引擎
config.json                配置：网盘地址/账号/密码/同步目录/间隔  ← 含密码
rclone.conf                由面板生成（含加密后的密码）
state\  logs\  jobs\       同步状态 / 日志 / 过滤规则缓存
```

> 分发时**不要**把 `config.json`、`rclone.conf`、`state/logs` 一起发出去（含你的账号密码）。
> 对方运行后会在其本机生成自己的配置。

## 从源码构建

需要 Go 1.21+。把 `rclone.exe` 放到 `ui/rclone/`（或 `dist/rclone/`）：

```bash
cd ui
go build -trimpath -ldflags "-s -w -H windowsgui" -o openlist-sync-ui.exe .
go test ./...
```

`-H windowsgui` 关键：否则会弹黑色控制台窗口。

## 源码结构

```
main.go                   HTTP 服务 / 配置 / 任务 / 同步编排
filters.go                .nosy(gitignore) → rclone 过滤规则翻译（含单测）
picker_windows.go         系统多选文件夹对话框（PowerShell 兜底）
com_picker_windows.go     进程内 COM(IFileOpenDialog) 选择器（首选）
tray_windows.go           系统托盘（无窗口常驻）
dpi_windows.go            Per-Monitor V2 DPI 感知（对话框不模糊）
single_instance_windows.go 单实例互斥量
task_windows.go           Windows 平台辅助（打开路径等）
web/index.html            单页界面（go:embed 内嵌进 exe）
```
