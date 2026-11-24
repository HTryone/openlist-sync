#!/usr/bin/env bash
# =============================================================================
# openlist-bisync.sh — 本地工作区 <-> OpenList 双向定期同步（rclone bisync）
#
# 设计目标对应需求：
#   1. 本地始终是完整副本，Agent 读写全走本地路径（无实时跨网络）
#   2. 定期双向同步（cron / 计划任务），弱网可重试
#   3. 本地新增/修改 -> 上传 OpenList
#   4. 远端新增/修改 -> 拉到本地
#   5. 本地删除 -> 远端删除，被删文件进垃圾桶归档
#   6. 垃圾桶在同步目标之外，不下发、不被清理
#
# 关键：需要 --backup-dir1 + --backup-dir2 两个垃圾桶
#   --backup-dir2 <目标>/.trash/<名>  捕获「本地删除」在远端被删掉的文件
#   --backup-dir1 <本地垃圾桶>              捕获「远端删除」在本地被删掉的文件
#   （只给 backup-dir2 时，别的 Agent 在远端删文件，本地副本会被直接删掉、无归档）
#
# 用法：
#   ./openlist-bisync.sh            # 常规双向同步（定时任务调用）
#   ./openlist-bisync.sh dry-run    # 预演，不落地任何改动
#   ./openlist-bisync.sh resync     # 首次建立基线（会做 dry-run 确认，慎用）
#   ./openlist-bisync.sh status     # 查看上次运行结果
# =============================================================================
set -uo pipefail

# ---------- 路径（全部可用环境变量覆盖，便于测试/多实例） ----------
LOCAL_DIR="${OPENLIST_BISYNC_LOCAL:-/work/workspace}"                 # Path1：本地工作区（只放工作文件）
REMOTE_PATH="${OPENLIST_BISYNC_REMOTE:-openlist:/ty2/agents-sy/workspace}"   # Path2：OpenList 同步目标
TRASH_LOCAL="${OPENLIST_BISYNC_TRASH_LOCAL:-/work/.trash_local}"      # backup-dir1：本地垃圾桶（同步源之外）
TRASH_REMOTE="${OPENLIST_BISYNC_TRASH_REMOTE:-openlist:/ty2/agents-sy/.trash/workspace}" # backup-dir2：远端垃圾桶（目标之外，同级）
WORKDIR="${OPENLIST_BISYNC_WORKDIR:-/work/.bisync-state}"             # bisync 状态目录（本地）
LOGFILE="${OPENLIST_BISYNC_LOG:-/var/log/openlist-bisync.log}"
RCLONE="${RCLONE_BIN:-rclone}"

# ---------- 弱网参数（详见 docs/弱网调参.md） ----------
RETRIES="${BISYNC_RETRIES:-10}"
LOW_LEVEL_RETRIES="${BISYNC_LOW_LEVEL_RETRIES:-20}"
CONTIMEOUT="${BISYNC_CONTIMEOUT:-30s}"
TIMEOUT="${BISYNC_TIMEOUT:-5m}"
MAX_DELETE="${BISYNC_MAX_DELETE:-50}"
CHECKERS="${BISYNC_CHECKERS:-8}"
TRANSFERS="${BISYNC_TRANSFERS:-4}"

MODE="${1:-sync}"

log() { echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*"; }

# ---------- 前置检查 ----------
command -v "$RCLONE" >/dev/null 2>&1 || { log "FATAL: 找不到 rclone（$RCLONE）"; exit 127; }
[ -d "$LOCAL_DIR" ] || { log "FATAL: 本地目录不存在: $LOCAL_DIR"; exit 2; }
mkdir -p "$WORKDIR" "$TRASH_LOCAL" "$(dirname "$LOGFILE")" 2>/dev/null || true

# 防止定时任务重叠运行（bisync 自带锁，这里再加一层 shell 锁，双保险）
if command -v flock >/dev/null 2>&1; then
  exec 9>"$WORKDIR/.runlock"
  flock -n 9 || { log "SKIP: 已有同步在运行"; exit 0; }
fi

# ---------- 公共参数 ----------
COMMON=(
  --checksum                       # 按内容哈希判断变化（需 WebDAV 暴露 oc:checksums，见调参文档）
  --backup-dir1 "$TRASH_LOCAL"     # 远端删除 -> 本地副本归档到这里
  --backup-dir2 "$TRASH_REMOTE"    # 本地删除 -> 远端副本归档到这里
  --workdir "$WORKDIR"
  --recover                        # 中断后自动恢复，无需 resync
  --max-delete "$MAX_DELETE"       # 删除比例安全阀（默认50%），防"全删"事故
  --retries "$RETRIES"
  --low-level-retries "$LOW_LEVEL_RETRIES"
  --contimeout "$CONTIMEOUT"
  --timeout "$TIMEOUT"
  --checkers "$CHECKERS"
  --transfers "$TRANSFERS"
  --log-file "$LOGFILE"
  --log-level INFO
  --stats 5m
)

run_bisync() {
  "$RCLONE" bisync "$LOCAL_DIR" "$REMOTE_PATH" "${COMMON[@]}" "$@"
  return $?
}

case "$MODE" in
  sync)
    log "开始双向同步: $LOCAL_DIR  <->  $REMOTE_PATH"
    if run_bisync; then
      log "OK: 同步成功"
      exit 0
    else
      rc=$?
      log "FAIL: 同步失败 rc=$rc（下次定时任务会自动重试；详见 $LOGFILE）"
      exit "$rc"
    fi
    ;;
  dry-run)
    log "DRY-RUN 预演（不落地）"
    run_bisync --dry-run
    exit $?
    ;;
  resync)
    log "== 首次基线 resync =="
    log "第 1 步：dry-run 预演，确认冲突副本行为"
    run_bisync --resync --dry-run || { log "dry-run 失败，中止"; exit 1; }
    read -r -p "预演无误？输入 YES 正式执行 --resync（会以本地为基准 path1 覆盖远端冲突）: " ans
    [ "$ans" = "YES" ] || { log "已取消"; exit 1; }
    log "第 2 步：正式 --resync"
    if run_bisync --resync; then log "OK: 基线已建立，此后去掉 resync 走常规 sync"; exit 0
    else log "FAIL: resync 失败 rc=$?"; exit 1; fi
    ;;
  status)
    if [ -f "$LOGFILE" ]; then tail -n 30 "$LOGFILE"; else log "暂无日志: $LOGFILE"; fi
    exit 0
    ;;
  *)
    echo "用法: $0 [sync|dry-run|resync|status]"; exit 64
    ;;
esac


