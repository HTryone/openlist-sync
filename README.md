# 网盘同步（OpenList ↔ 本地）

像软件一样：双击 `ui/启动.cmd`（或 `ui/openlist-sync-ui.exe`）→ 右下角托盘图标、自动打开
配置面板 http://127.0.0.1:5399/ ，后台按间隔自动双向同步，本地删除进网盘 `.trash/<文件夹名>`。

## 目录

- `ui/` —— Go 应用（配置面板 + 内置定时同步 + 系统托盘）
  - `main.go` 服务/配置/同步编排；`filters.go` `.nosy`(gitignore)→rclone 过滤翻译
  - `picker_windows.go` 系统多选文件夹对话框（含 DPI 感知）；`tray_windows.go` 托盘；`dpi_windows.go`
  - `web/index.html` 单页界面（内嵌进 exe）
- `deploy/` —— 早期 Linux systemd / cron / 脚本部署（备选）
- `docs/` —— 验证清单、弱网调参、备选方案

## 构建（需自带 rclone）

```bash
# 把 rclone.exe 放到 ui/rclone/rclone.exe（工具会优先使用它，免额外安装）
cd ui
go build -trimpath -ldflags "-s -w -H windowsgui" -o openlist-sync-ui.exe .
go test ./...
```

## 使用

1. 填网盘地址/账号/密码，「网盘目录」选上一级（如 `/ty2`），系统自动在其下建 `agents-sy`
2. 「+ 添加一行 / 多选添加」用**系统文件夹对话框**选本地文件夹 → 自动同步到 `agents-sy/<文件夹名>`
3. 左侧「自动同步」设间隔/开关 → 保存，即按间隔自动双向同步

## 约定

- 一个本地文件夹 = `agents-sy/` 下一个同名子目录
- 本地删除 → 远端删除，归档到 `agents-sy/.trash/<文件夹名>/`（各自独立）
- 每个文件夹根可放 `.nosy`（gitignore 语法）过滤不参与同步的内容
- 比较方式 `size-only`（本服务端无哈希、时间不可靠）

> 本仓库不含 `config.json`（含密码）、`rclone.exe`、运行日志等。
