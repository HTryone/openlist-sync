# D:\newsky\sy 记忆

> 这个文件夹是 **OpenList 网盘双向同步工具**（Windows Go 托盘应用 + Linux rclone 部署）的开发仓库。

| 文件夹 | 是什么 |
|---|---|
| `ui\` | Go 桌面应用：托盘 + 配置面板 + 内置定时同步 + 网盘挂载（源码、构建产物、运行时目录都在这） |
| `deploy\` | Linux 服务器命令行部署（rclone bisync + systemd/cron） |
| `docs\` | 产品文档：产品介绍 / 验证清单 / 弱网调参 / 备选方案 |
| `.tmp\` | 临时验证脚本（不入 git） |
| `dist\` | 分发包（OpenListSync.exe + rclone，不入 git） |

## 重大事件（只记这几条）

- GitHub 公开仓库：https://github.com/HTryone/openlist-sync（MIT，CODEOWNERS=@HTryone）
- 分发方式：只发 `dist\`（exe+自带 rclone，不含 config.json/密码）

## 本层工具

| 位置 | 内容 |
|---|---|
| `ui\openlist-sync-ui.exe` | 主程序（构建产物，勿手编） |
| `ui\rclone\rclone.exe` | 自带同步引擎 v1.76.0 |

