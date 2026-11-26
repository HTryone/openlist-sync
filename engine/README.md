# OpenListSync 引擎（headless）

本目录是 OpenListSync 的**同步引擎**——纯 CLI 的 rclone bisync 封装，无图形界面、无托盘、无 Web 面板。

它有两个使用者：

1. **OpenList Desktop**（`../desktop/`）：把本引擎编译为 `openlistsync` sidecar，通过命令行调用完成同步。
2. **高级用户 / 计划任务**：直接在命令行运行，做无人值守同步。

## 命令

```text
# 执行一轮全部启用目录的同步后退出（桌面端/计划任务用）
openlistsync -base <数据目录> -run [-job <任务ID>]

# 常驻后台：内置调度器按间隔持续同步（桌面端退出后不中断）
openlistsync -base <数据目录> -serve
```

| 参数 | 说明 |
|---|---|
| `-base` | 数据目录：`config.json`、`rclone.conf`、logs/state/recycle 都放这里。默认 exe 所在目录 |
| `-run` | 执行一轮同步后退出；任一任务失败退出码 1，任务不存在退出码 2 |
| `-job` | 只执行指定任务 ID（配合 `-run`） |
| `-serve` | 常驻模式：启动后按 `config.json` 里 `globals.intervalMin` 循环同步，单实例锁防重复启动 |

## 数据目录约定

```text
<base>/config.json   配置（含 WebDAV 连接与任务列表）
<base>/rclone.conf   引擎私有 rclone 配置（不碰全局配置）
<base>/logs/         每任务同步日志
<base>/state/        bisync 基线
<base>/recycle/      网盘侧删除的暂存区
<base>/trash/        无法进系统回收站时的本地留档
```

## 同步行为要点

- 底层 rclone bisync；删除归档到网盘 `.trash/`，本地侧删除尽量进系统回收站（见 `recycle.go`）
- 每个本地目录根可放 `.nosy`（gitignore 语法）过滤
- 首次运行自动建基线；首次大目录先并行 copy 铺底再 resync（见 `seedAndResync`）
- 针对无哈希、modtime 不可靠的 WebDAV 用 `--size-only`

## 构建

```powershell
cd engine
go test ./...
$env:CGO_ENABLED='0'
go build -trimpath -ldflags "-s -w -H windowsgui" -o ../dist/OpenListSync.exe .
```

桌面端构建脚本 `desktop/scripts/build-openlistsync.ps1` 从本目录源码产出 `openlistsync-<target>.exe` sidecar。

## 目录结构

```text
main.go        配置模型 / CLI 入口 / 调度器 / bisync 参数 / 任务执行
filters.go     .nosy(gitignore) -> rclone filter 转换
recycle*.go    暂存区 + 系统回收站（Windows SHFileOperation）
job_windows.go Windows Job Object（子进程随引擎退出）
task_windows.go rclone 子进程隐藏窗口