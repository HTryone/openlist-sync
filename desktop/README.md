# OpenList Desktop 集成快照

本目录保存 OpenList Desktop（Tauri 2 + Vue 3）接入 OpenListSync 的集成层代码快照。

OpenListSync 本体仍然是 `../ui/` 中的 Go 源码；这里不放同步引擎源码，只放桌面端如何调用引擎的参考实现。

## 目录对应关系

将本目录中的文件按相对路径覆盖到 OpenList Desktop 仓库：

- `src-tauri/src/cmd/sync.rs` → `src-tauri/src/cmd/sync.rs`
- `src/views/SyncView.vue` → `src/views/SyncView.vue`
- `src/api/tauri.ts` → `src/api/tauri.ts`
- `src/router/index.ts` → `src/router/index.ts`
- `src/components/NavigationPage.vue` → `src/components/NavigationPage.vue`
- `src/types/types.d.ts` → `src/types/types.d.ts`
- `src/i18n/locales/zh.json` → `src/i18n/locales/zh.json`
- `src/i18n/locales/en.json` → `src/i18n/locales/en.json`

集成时还需要在 OpenList Desktop 中：

- 在 `src-tauri/src/cmd/mod.rs` 声明 `pub mod sync;`
- 在 `src-tauri/src/lib.rs` 注册 `sync_*` commands 并启动 `spawn_auto_sync`
- 将 `openlistsync` 加入 Tauri `externalBin`
- 构建 `openlistsync-<target>` sidecar 随安装包分发

## Sidecar 接口

桌面端通过独立进程调用同步引擎，不把 Go 引擎合入 Tauri 主进程：

```text
openlistsync -base <数据目录> -run [-job <任务ID>]
```

- `-base`：同步配置、日志、状态、回收站、rclone.conf 的隔离目录
- `-run`：无界面执行一次同步后退出
- `-job`：只执行指定任务，空则执行所有启用任务

## 数据隔离

桌面端传递的 `<数据目录>` 通常是：

```text
%APPDATA%\OpenList Desktop\sync
```

该目录只保存同步引擎数据，不保存 OpenList 核心数据。