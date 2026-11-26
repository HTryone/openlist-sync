# OpenList 网盘同步

把多个本地文件夹像"上传网盘"一样，定期**双向同步**到 [OpenList](https://github.com/OpenListTeam/OpenList)（WebDAV）。
本地始终保有完整副本（读写不跨网），后台按间隔自动同步，删除归档到网盘，不引入 Git / 锁服务 / 重型协作平台。

## 两条使用路径

| 场景 | 方案 | 入口 |
|---|---|---|
| **Windows 桌面（推荐日常用）** | 图形面板 + 系统托盘 + 内置定时同步 | [`ui/`](ui/README.md) → 分发包 [`dist/`](ui/README.md) |
| **Linux 服务器（无图形界面）** | 纯 rclone 命令行 + systemd/cron | [`deploy/`](deploy/) |

两者底层都是 **rclone bisync**，目录约定一致。

## Windows 桌面端（ui/）

双击 `OpenListSync.exe` → 右下角托盘图标 + 自动打开面板 `http://127.0.0.1:5399/`，无黑色命令行窗口，单实例。

- 多个本地文件夹，各自同步到 `网盘目录/agents-sy/<文件夹名>`
- 系统文件夹对话框选目录（含 DPI 感知）；选完**自动在网盘建好目录**
- 每个文件夹根可放 `.nosy`（gitignore 语法）过滤
- 本地删除 → 网盘删除并归档到 `agents-sy/.trash/<文件夹名>/`
- 自包含：自带 `rclone/rclone.exe`，无需另装

详见 [ui/README.md](ui/README.md)。

## Linux 服务器端（deploy/）

无图形界面的服务器上，用命令行 + systemd timer（或 cron）定期双向同步：

```bash
sudo ./deploy/install-linux-systemd.sh   # 装 rclone、建目录、注册 systemd timer（每 30 分钟）
# 或参考 deploy/crontab.example 用 cron
```

核心脚本 `deploy/openlist-bisync.sh` 支持 `sync | dry-run | resync | status`。
弱网下 bisync 不稳时，用 `deploy/openlist-fallback.sh`（单向 sync + copy 预取）。
配置模板见 `deploy/rclone.conf.example`。

## 目录约定（两端一致）

```
本地/服务器工作区  →  openlist:/ty2/agents-sy/<文件夹名>
删除归档          →  openlist:/ty2/agents-sy/.trash/<文件夹名>/
```

- 一个本地文件夹 = `agents-sy/` 下一个同名子目录
- 垃圾桶在同步目标**之外**（各自独立），不参与同步、不被二次清理
- 比较方式 **size-only**（服务端无哈希、时间不可靠）；**大小不变仅改内容检测不到**
- 不引入 Git / 锁服务 / 重型协作平台

## 文档

- [docs/产品介绍.md](docs/产品介绍.md) —— 产品定位、核心设计、适用场景\n- [ui/README.md](ui/README.md) —— Windows 桌面端：使用、配置位置、构建、源码结构
- [docs/验证清单.md](docs/验证清单.md) —— 上传前必做的核对步骤
- [docs/弱网调参.md](docs/弱网调参.md) —— 重试/超时/并发参数
- [docs/备选方案.md](docs/备选方案.md) —— bisync 不稳时的单向方案

## OpenList Desktop 集成

本仓库同时保留桌面端集成层快照：[`desktop/`](desktop/)。

OpenList Desktop 以 sidecar 方式调用本引擎：

```text
openlistsync -base <数据目录> -run [-job <任务ID>]
```

这种方式保证桌面 UI 与同步引擎解耦：引擎仍由 `ui/` 中的 Go 源码构建成独立 exe，随桌面端安装包分发，而配置、日志、状态和回收站由 `-base` 指定目录隔离。
## 仓库结构

```
ui/       Windows 桌面应用（Go）
deploy/   Linux 服务器部署（rclone + systemd/cron）
docs/     文档
THIRD-PARTY-NOTICES.md  第三方组件与许可声明（须随分发包一起分发）
```

## 说明

- 本仓库**不含**：`OpenListSync.exe`、`rclone.exe`、`config.json`（含密码）、运行日志等（见 `.gitignore`）。
- 分发包由 `ui/` 构建产出，放 `dist/`，不入库。

## License

本仓库代码 [MIT](LICENSE) © HTryone。

随分发包分发的第三方组件：

- **rclone** v1.76.0（`rclone/rclone.exe`，MIT）
- **Go** 运行时与标准库（编译进 exe，BSD-3-Clause）
- **WinFsp**（仅挂载功能需要，用户自行安装，**不随包分发**，GPLv3 + FLOSS 例外）
  —— WinFsp - Windows File System Proxy, Copyright (C) Bill Zissimopoulos，
  见 [winfsp/winfsp](https://github.com/winfsp/winfsp)

完整声明与各许可全文见 [THIRD-PARTY-NOTICES.md](THIRD-PARTY-NOTICES.md)。


