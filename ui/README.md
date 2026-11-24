# 网盘同步（OpenList ↔ 本地）

像软件一样用：**双击 `启动.cmd`（或 `openlist-sync-ui.exe`）** →
右下角出现托盘图标、自动打开配置页面 http://127.0.0.1:5399/ ，后台按间隔自动双向同步。
**没有黑色命令行窗口**。关闭：托盘图标右键「退出」，或双击 `停止.cmd`。

## 自包含
工具文件夹里已带 `rclone/rclone.exe`，**无需另外安装 rclone**。整个文件夹拷走即可用。

## 怎么用（3 步）
1. 填「网盘地址 / 账号 / 密码」，点 **保存设置**、**测试连接**
2. 「网盘同步目录」点 **浏览网盘…**，在 WebDAV 里点选一个目录（如 `openlist:/ty2/agents-sy`），保存
   —— 也可以直接手写这个路径
3. 点 **+ 添加一行** 或 **多选添加**，在弹出的**网页内文件夹选择器**里选本地文件夹
   —— 每个本地文件夹会自动同步到 `网盘同步目录/<文件夹名>`

之后交给自动同步（每 N 分钟）。首次会自动建立基线。

## 目录对应
- 本地 `D:\项目\甲方` → 网盘 `openlist:/ty2/agents-sy/甲方`
- 一个本地文件夹 = 网盘同步目录下的一个同名子目录

## 选择器（都在网页里，清晰不模糊）
- **本地文件夹选择器**：盘符 → 逐级进入 → 勾选（可多选）
- **网盘目录选择器**：在 WebDAV 里逐级浏览 → 选这个目录；也支持地址栏手写

## `.nosy` 过滤（每个本地文件夹根目录）
gitignore 语法，排除不想同步的内容：
```
node_modules/
build/
dist/
*.tmp
.env
# 排除整个仓库文件夹
wujieapp/apps/
 docs/*
!docs/README.md
```
`.nosy` 本身不参与同步。点每个文件夹的「过滤规则 .nosy」可直接编辑。

## 参数（已按本机 OpenList/天翼云盘校准）
- **按大小比较**：服务端不暴露哈希、时间不可靠，故 `--size-only`。
  局限：大小不变、只改内容 → 检测不到。
- **串行传输**：天翼云盘并发易 404；首次大目录会先并行铺底再建基线。
- **双垃圾桶**：本地删除→远端 `_trash_*`；远端删除→本地 `trash/` 目录。

## 目录结构（工具文件夹）
```
openlist-sync-ui.exe   主程序（无窗口，托盘常驻）
rclone/rclone.exe      自带的同步引擎
config.json            配置（账号密码、目录、间隔）
rclone.conf            由面板生成的 rclone 配置
state/  logs/  jobs/   同步状态 / 日志 / 过滤规则缓存
trash/                 本地垃圾桶
启动.cmd  停止.cmd
web/index.html         界面（已内嵌进 exe）
main.go  filters.go  task_windows.go  tray_windows.go  picker_windows.go
```

## 源码编译
```
go build -trimpath -ldflags "-s -w -H windowsgui" -o openlist-sync-ui.exe .
go test ./...
```
