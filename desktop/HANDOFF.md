# OpenList-Desktop 二次开发交接文档

> 交接时间：2026-11-26 23:40 (Asia/Shanghai)
> 交接原因：UI 需求反复返工，改由他人接手
> 代码位置：`D:\newsky\OpenList-Desktop`（HTryone/OpenList-Desktop 的 fork）

---

## 一、项目背景与目标

原项目 [OpenList-Desktop](https://github.com/OpenListTeam/OpenList-Desktop)（v0.9.1，Tauri 2 + Vue 3 + Rust，**GPL-3.0**）只做两件事：

1. 管理 OpenList 核心进程（启停、日志、版本）
2. 通过 rclone 把网盘**挂载**成盘符

**缺的是「双向同步」**。已有的独立工具 `D:\newsky\sy`（Go 写的 OpenListSync，走 rclone bisync）实现了同步功能，目标是把它并进桌面端，做一个「挂载 + 同步」二合一的软件。

架构选型：**不改写引擎**，把 Go 引擎当 Tauri sidecar 调用（`openlistsync.exe -base <dir> -run`），UI 只负责配置和展示。

---

## 二、当前完成状态

### ✅ 已完成并且验证通过

| 模块 | 文件 | 说明 |
|---|---|---|
| 同步后端命令 | `src-tauri/src/cmd/sync.rs`（新增，287 行） | 5 个 tauri command + 1 个定时器 |
| 命令注册 | `src-tauri/src/lib.rs` | 引入 + `generate_handler!` 注册 + `spawn_auto_sync` 启动 |
| 模块声明 | `src-tauri/src/cmd/mod.rs` | `pub mod sync;` |
| 同步前端页面 | `src/views/SyncView.vue`（新增，394 行） | 连接配置 / 目录管理 / 定时同步 / 运行输出 |
| 路由 | `src/router/index.ts` | `/sync` 路由 |
| 导航 | `src/components/NavigationPage.vue` | 侧栏第 2 项「同步」 |
| 类型定义 | `src/types/types.d.ts` | SyncJob / SyncGlobals / SyncConfig / SyncRunResult / SyncEngineInfo |
| 前端 API | `src/api/tauri.ts` | `TauriAPI.sync.*` 五个方法 |
| i18n | `src/i18n/locales/{zh,en}.json` | `sync.*` 与 `mount.config.*` 新增键 |
| 引擎新增参数 | `D:\newsky\sy\ui\main.go` | `-base`（数据目录）/ `-job`（只跑单个任务） |

验证结果：
- `cargo check` ✅ 通过
- `vue-tsc --noEmit` ✅ 0 错误
- `eslint` ✅ 通过
- `SFC parse` ✅ 通过

### ⚠️ 半成品（本次交接的核心）

**`src/views/MountView.vue` 的「额外标志」区域已改成双 Tab 结构，但还有未完成项。**

已实现：
- 「常用标志」/「额外标志」两个大标题 Tab（`flagTab` 状态，行 704）
- 常用标志 Tab：视频播放模式方块 + 三列分类面板（缓存/性能/宽带…）
- 额外标志 Tab：添加标志按钮 + 参数行
- 视频模式三个参数已从只读文字改为**可编辑数字输入框**（行 494-506）
- 原来那个「常用标志」折叠按钮已删除（避免和 Tab 重名）
- 原来那条「点击即可立即添加或移除」提示条已删除（用户明确要求删）

**未完成（用户最后一个明确需求）：**
> **分类面板里的每个参数（如 `--vfs-read-chunk-size=128M`）也要能改数字值**，不只是勾选/取消。

---

## 三、未完成需求的实现方案

### 需求原话
> 「可以设置预设但是我应该可以自己改呀，我可以改默认数字呀」
> 追问后确认：**指分类面板里的参数想改数值**（不是视频模式那三个）

### 现状分析

`MountView.vue` 里分类面板每个 flag 的结构是：

```vue
<div v-for="flag in category.flags" @click="toggleFlag(flag)">
  <div class="cb" :class="{on: isFlagInConfig(flag)}"></div>
  <code>{{ flag.flag }}{{ flag.value ? `=${flag.value}` : '' }}</code>
  <span>{{ getFlagDescription(flag) }}</span>
</div>
```

配套函数（行 1126-1168）：

| 函数 | 作用 |
|---|---|
| `addFlagToConfig(flag)` | 把 `${flag.flag}=${flag.value}` 推入 `extraFlags` |
| `removeFlagFromConfig(flag)` | 按完整字符串 `indexOf` 删除 |
| `isFlagInConfig(flag)` | 判断完整字符串是否存在 |
| `toggleFlag(flag)` | 上面两个的二选一 |

**核心问题**：整套逻辑基于**完整字符串精确匹配**（`--vfs-read-chunk-size=128M`）。一旦用户把值改成 `256M`，`isFlagInConfig` 就找不到它，勾选状态会丢失。

### 推荐改法

把「按完整字符串匹配」改成「**按 flag 名匹配，值可覆盖**」：

```ts
// 1. 新增：按 flag 名取当前值
const getFlagValue = (flag: {flag: string; value: string}) => {
  const cur = (configForm.value.extraFlags || []).find(x => x === flag.flag || x.startsWith(flag.flag + '='))
  if (!cur) return ''
  return cur.includes('=') ? cur.slice(cur.indexOf('=') + 1) : ''
}

// 2. 新增：改值时替换整条
const setFlagValue = (flag: {flag: string; value: string}, v: string) => {
  if (!configForm.value.extraFlags) configForm.value.extraFlags = []
  const idx = configForm.value.extraFlags.findIndex(x => x === flag.flag || x.startsWith(flag.flag + '='))
  const val = v.trim()
  if (!val) {
    // 空值 = 移除
    if (idx >= 0) configForm.value.extraFlags.splice(idx, 1)
    return
  }
  const next = `${flag.flag}=${val}`
  if (idx >= 0) configForm.value.extraFlags[idx] = next
  else configForm.value.extraFlags.push(next)
}

// 3. 改 isFlagInConfig：从精确匹配改为前缀匹配
const isFlagInConfig = (flag) =>
  (configForm.value.extraFlags || []).some(x => x === flag.flag || x.startsWith(flag.flag + '='))

// 4. 改 removeFlagFromConfig：同样改前缀匹配
```

模板里把 `<code>` 换成「flag 名 + 输入框」：

```vue
<div class="flex items-center gap-1.5">
  <code class="...">{{ flag.flag }}</code>
  <span v-if="flag.value" class="text-secondary">=</span>
  <input
    v-if="flag.value"
    :value="getFlagValue(flag)"
    :disabled="!isFlagInConfig(flag)"
    class="w-20 rounded border ..."
    @input="setFlagValue(flag, ($event.target as HTMLInputElement).value)"
  />
</div>
```

**注意事项**：
- `--vfs-cache-mode`、`--buffer-size`、`--log-level` 这三个在 `addFlagToConfig` 里有「互斥替换」逻辑（行 1133-1138），改值时不要破坏
- 空字符串参数的 flag（如 `--read-only`、`--verbose`）没有 `=`，要跳过输入框
- 改完必须跑 `vue-tsc --noEmit` 和 `npx eslint src/views/MountView.vue`

---

## 四、环境与运行方式

### 依赖（已就绪）
```
Node v24.14.0
yarn 1.22.22（已全局安装）
rustc / cargo 1.98.0
go 1.27.4
```

### 启动开发环境
```powershell
cd D:\newsky\OpenList-Desktop
yarn tauri dev
```

**常见坑：**
1. `yarn` 找不到 → 已全局装好，若失效执行 `npm install -g yarn`
2. **端口 5173 被占** → 上次 dev 没退干净，执行：
   ```powershell
   Get-NetTCPConnection -State Listen -LocalPort 5173 | % { Stop-Process -Id $_.OwningProcess -Force }
   ```
3. 窗口不显示 → `tauri.conf.json` 已把 `"visible"` 改成 `true`（原本是 false，由 JS 控制显示，dev 模式下容易不弹）
4. 改 Rust 代码必须重启 dev；改 Vue 是热更新

### sidecar 二进制必须放在这里
```
src-tauri\target\debug\openlistsync.exe   ← Go 同步引擎（从 D:\newsky\sy\ui 编译）
src-tauri\target\debug\rclone.exe         ← rclone
```
当前这两个文件已存在。重新编译引擎：
```powershell
cd D:\newsky\sy\ui
$env:CGO_ENABLED='0'
go build -trimpath -ldflags "-s -w -H windowsgui" -o "D:\newsky\OpenList-Desktop\src-tauri\target\debug\openlistsync.exe" .
```

### 打包时
`src-tauri/tauri.conf.json` 的 `externalBin` 目前只有 `["binary/openlist"]`，**需要加上 rclone 和 openlistsync**，并在 `src-tauri/binary/` 放对应文件（命名规则 `xxx-x86_64-pc-windows-msvc.exe`）。

---

## 五、同步功能的数据流（重要）

```
用户点「同步」
  → TauriAPI.sync.run(jobId)
  → Rust: sync_run → Command::new(openlistsync.exe) -base <APPDATA>/OpenList Desktop/sync -run [-job <id>]
  → Go 引擎读 <base>/config.json，跑 rclone bisync
  → 输出 return 给前端展示
```

**目录隔离**（用户明确要求）：
- 引擎数据全在 `%APPDATA%\OpenList Desktop\sync\`（config.json / logs / state / recycle / trash）
- **不含**任何 OpenList 核心数据
- `.gitignore` 已加 `.tmp/` 和 `sync-data/`

**定时自动同步**：`spawn_auto_sync` 每 20 秒读一次配置，到 `intervalMin` 就跑一轮。开关在 SyncView 里，写的是引擎 config.json 的 `globals.autoEnabled`。

---

## 六、用户明确表达过的约束（务必遵守）

1. **禁止擅自改代码** —— 必须先出方案/草图，用户说「改」才动手
2. **加功能不能删旧功能** —— 加 Tab 不代表可以删原有按钮、图标、提示文字
3. **不要改文案** —— `常用标志`、`额外标志`、`quickFlags` 等原文案不得擅自更换
4. **不要自作主张加东西** —— 说改一个地方就只改一个地方
5. **渲染图必须用真实设计 token**：
   - 主色 `#007aff`（`--color-accent`）
   - 主文字 `#1d1d1f`（`--color-text-primary`）
   - 次文字 `#6e6e73`（`--color-text-secondary`）
   - 底色 `#f5f5f7`（`--color-background-secondary`）
   - 边框 `rgba(0,0,0,.1)`（`--color-border`）

---

## 七、本次交接前踩过的坑（引以为戒）

| 坑 | 后果 | 正确做法 |
|---|---|---|
| 用批量行替换挪动 Vue 模板块 | `<div>` 层级切坏，反复补标签造成 5+ 次语法错误 | 用 `edit` 工具做定点替换，改完立刻跑 SFC 解析器 |
| 只信任 `vue-tsc` 判断模板正确性 | `vue-tsc` 对模板结构错误不敏感，漏报 | 用 `node -e "require('@vue/compiler-sfc').parse(...)"` 验证 |
| 改完没重启 dev 就说「已生效」 | 用户看到的还是旧界面 | 改 Rust 必须重启；改 Vue 确认热更新或手动刷新 |
| 用 `Set-Content` 改写含中文的 .vue | 会带 BOM 或 CRLF，eslint 报一堆 `Delete ␍` | 用 `[IO.File]::WriteAllText` 统一成 LF |
| 一次改多处 | 出错后无法定位是哪处引起的 | 一次只改一个点，验证后再下一个 |

---

## 八、当前 git 状态

```
分支：main（基于 upstream bc3389c）
远程：origin = https://github.com/HTryone/OpenList-Desktop.git

已修改（未提交）：
  M .gitignore
  M src-tauri/Cargo.toml          ← 仅行尾差异，内容与 HEAD 一致
  M src-tauri/src/cmd/mod.rs
  M src-tauri/src/lib.rs
  M src-tauri/tauri.conf.json     ← visible: false → true
  M src/api/tauri.ts
  M src/components/NavigationPage.vue
  M src/i18n/locales/en.json
  M src/i18n/locales/zh.json
  M src/router/index.ts
  M src/types/types.d.ts
  M src/views/MountView.vue
新增（未跟踪）：
  ?? src-tauri/src/cmd/sync.rs
  ?? src/views/SyncView.vue
```

**未提交、未推送。** 建议接手后先 `git diff` 全部过一遍再决定提交粒度（建议拆两个 commit：①同步功能 ②MountView Tab）。

---

## 九、下一步优先级建议

1. **P0**：完成「分类面板参数可改数值」（第三节的方案）
2. **P1**：验证同步全链路 —— 启动 OpenList 核心（`D:\OpenListBuilder-windows-amd64\start-openlist.vbs`，5244 端口）→ SyncView 填连接 → 添加目录 → 首次同步 → 改动 → 再同步
3. **P2**：打包配置（externalBin 加 rclone + openlistsync）
4. **P3**：清理 `D:\newsky\OpenList-Desktop\.tmp\` 下 29 个调试文件（9 版 mockup、AST 脚本等）

---

## 十、参考文件

| 用途 | 路径 |
|---|---|
| 引擎源码 | `D:\newsky\sy\ui\main.go`（1670 行） |
| 引擎回收站功能 | `D:\newsky\sy\ui\recycle*.go` |
| 引擎验证清单 | `D:\newsky\sy\docs\验证清单.md` |
| 引擎 README | `D:\newsky\sy\README.md` |
| 挂载页原代码 | `src/views/MountView.vue`（1250 行） |
| 同步后端 | `src-tauri/src/cmd/sync.rs` |
| OpenList 安装位置 | `D:\OpenListBuilder-windows-amd64\`（含 start-openlist.vbs） |
