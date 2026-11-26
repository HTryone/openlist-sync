# OpenList Desktop（已合并进 OpenListSync 仓库）

本目录是完整的 OpenList Desktop 源码（Tauri 2 + Vue 3 + Rust），已经与 `ui/` 中的 OpenListSync 同步引擎合并到同一个 Git 仓库中。

## 架构

- `ui/`：OpenListSync Go 同步引擎源码（sidecar）
- `desktop/`：OpenList Desktop 桌面端源码
  - Tauri 主程序
  - Vue 3 UI
  - 挂载管理
  - 同步集成（`src-tauri/src/cmd/sync.rs`、`src/views/SyncView.vue`）

同步引擎不作为 Tauri 主进程的一部分运行，而是由桌面端以 sidecar 方式调用：

```text
openlistsync -base <数据目录> -run [-job <任务ID>]
```

这样保留 Go 引擎独立进程隔离，同时源码和构建链统一到一个仓库。

## 开发

本目录使用自己的 Node 依赖：

```powershell
cd desktop
yarn install
yarn tauri dev
```

Go 引擎：

```powershell
cd ../ui
go test ./...
go build -trimpath -ldflags '-s -w -H windowsgui' -o ../dist/OpenListSync.exe .
```

## 打包

桌面端打包时会将 `openlistsync` 作为 sidecar 一起分发。`desktop/src-tauri/tauri.conf.json` 中的 `externalBin` 已包含 `openlistsync`。

构建产物目录（不会提交）：

```text
desktop/node_modules/
desktop/dist/
desktop/src-tauri/target/
desktop/src-tauri/binary/
```
