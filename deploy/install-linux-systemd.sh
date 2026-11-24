# =============================================================================
# 首次部署 / 上线步骤（Linux 服务器，systemd timer 方案，最稳）
# 顺序照做，每步都有验证点。所有命令以 bash / root 为参考。
# =============================================================================
set -euo pipefail

# ---------- 0. 装 rclone（>= v1.66，backup-dir1/2 才可用；建议 1.76+） ----------
curl -fsSL https://rclone.org/install.sh | sudo bash
rclone version | head -n1        # 验证 >= 1.66

# ---------- 1. 目录骨架 ----------
# 本地：只放工作文件；本地垃圾桶在同步源之外
sudo mkdir -p /work/workspace /work/_trash_local /work/.bisync-state /var/log
# OpenList 远端（用 WebDAV 客户端或 OpenList 后台创建）：
#   /backup/workspace   ← 同步目标
#   /trash/workspace    ← 垃圾桶，必须与 /backup/workspace 同级、在目标之外
#   两个目录都要建好，且不给 /trash 挂任何同步任务。

# ---------- 2. 配置远端 ----------
sudo mkdir -p ~/.config/rclone
cp rclone.conf.example ~/.config/rclone/rclone.conf
chmod 600 ~/.config/rclone/rclone.conf
# 改 url / user / pass（pass 用：rclone obscure '明文'）
rclone lsd openlist:                 # 验证连通
rclone lsd openlist:/backup
rclone lsd openlist:/trash

# ---------- 3. 确认 checksum 是否原生可用 ----------
rclone backend features openlist: | grep -i -A2 hash || true
# 或：放一个文件后  rclone hashsum md5 openlist:/backup/workspace/somefile
# 有哈希 -> --checksum 全量生效；无哈希 -> 见 docs/弱网调参.md 的说明

# ---------- 4. 部署同步脚本 ----------
sudo install -m 0755 openlist-bisync.sh /usr/local/bin/openlist-bisync.sh
# 按需在脚本顶部或 /etc/default/openlist-bisync 里改路径（默认见脚本注释）

# ---------- 5. 首次基线 resync（务必先预演） ----------
sudo LOCAL=sudo /usr/local/bin/openlist-bisync.sh dry-run   # 先看 diff 不落地
sudo /usr/local/bin/openlist-bisync.sh resync               # 交互式：先 dry-run 再确认 YES
# resync 会以本地 path1 为基准覆盖远端冲突；若远端已有独有文件，会在两个方向都留下

# ---------- 6. 装 systemd timer（每 30 分钟一次；可改 OnUnitActiveSec） ----------
sudo tee /etc/systemd/system/openlist-bisync.service >/dev/null <<'EOF'
[Unit]
Description=OpenList bisync (workspace <-> /backup/workspace)
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
Environment=OPENLIST_BISYNC_LOCAL=/work/workspace
Environment=OPENLIST_BISYNC_REMOTE=openlist:/backup/workspace
Environment=OPENLIST_BISYNC_TRASH_LOCAL=/work/_trash_local
Environment=OPENLIST_BISYNC_TRASH_REMOTE=openlist:/trash/workspace
Environment=OPENLIST_BISYNC_WORKDIR=/work/.bisync-state
Environment=OPENLIST_BISYNC_LOG=/var/log/openlist-bisync.log
ExecStart=/usr/local/bin/openlist-bisync.sh sync
Nice=10
IOSchedulingClass=best-effort
IOSchedulingPriority=7
EOF

sudo tee /etc/systemd/system/openlist-bisync.timer >/dev/null <<'EOF'
[Unit]
Description=Run OpenList bisync every 30 min

[Timer]
OnBootSec=5min
OnUnitActiveSec=30min
AccuracySec=1min
Persistent=true

[Install]
WantedBy=timers.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable --now openlist-bisync.timer
systemctl list-timers openlist-bisync.timer       # 验证下次触发时间
sudo systemctl start openlist-bisync.service      # 手动跑一次
sudo journalctl -u openlist-bisync -n 40 --no-pager
tail -n 40 /var/log/openlist-bisync.log           # 验证 "Bisync successful"
