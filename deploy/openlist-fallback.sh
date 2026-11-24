#!/usr/bin/env bash
# =============================================================================
# openlist-fallback.sh —— bisync 在弱网下不稳定时的退回方案（单向 + 预取）
#
# 语义更简单可控：
#   写侧：  rclone sync  本地工作区 -> OpenList（单向备份，含删除传播）
#   读侧：  rclone copy  OpenList 共享目录 -> 本地缓存（显式预取，只增量拉取）
#
# 与 bisync 的差别：
#   - 不是"完全镜像"：读侧是 copy（只增/改，不因远端删除而删本地缓存）
#   - 删除语义变成"写侧单向删除"；读侧删除请手工处理缓存
#   - 好处：无 resync 概念、无冲突副本、弱网下每次都是幂等的，不会把一次失败的
#           列表当成"全删"误判（配合 --max-delete 更稳）
#
# 垃圾桶：写侧删除用 --backup-dir 归档到 <备份目录>/.trash（目标之外）
#
# 用法：
#   ./openlist-fallback.sh           # 一次「推 + 拉」全流程
#   ./openlist-fallback.sh push      # 只推（本地 -> 远端）
#   ./openlist-fallback.sh pull      # 只拉（远端共享 -> 本地缓存）
# =============================================================================
set -uo pipefail

LOCAL_DIR="${OPENLIST_FB_LOCAL:-/work/workspace}"
REMOTE_PATH="${OPENLIST_FB_REMOTE:-openlist:/ty2/agents-sy/workspace}"
TRASH_REMOTE="${OPENLIST_FB_TRASH:-openlist:/ty2/agents-sy/.trash/workspace}"
SHARED_REMOTE="${OPENLIST_FB_SHARED:-openlist:/shared/docs}"
CACHE_DIR="${OPENLIST_FB_CACHE:-/work/_cache/shared}"
LOGFILE="${OPENLIST_FB_LOG:-/var/log/openlist-sync.log}"
RCLONE="${RCLONE_BIN:-rclone}"

RETRIES="${FB_RETRIES:-10}"
LOW_LEVEL_RETRIES="${FB_LOW_LEVEL_RETRIES:-20}"
CONTIMEOUT="${FB_CONTIMEOUT:-30s}"
TIMEOUT="${FB_TIMEOUT:-5m}"
MAX_DELETE="${FB_MAX_DELETE:-50}"

MODE="${1:-both}"
log() { echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*"; }
command -v "$RCLONE" >/dev/null 2>&1 || { log "FATAL: 找不到 rclone"; exit 127; }
[ -d "$LOCAL_DIR" ] || { log "FATAL: 本地目录不存在: $LOCAL_DIR"; exit 2; }
mkdir -p "$CACHE_DIR" "$(dirname "$LOGFILE")" 2>/dev/null || true

NET=(--retries "$RETRIES" --low-level-retries "$LOW_LEVEL_RETRIES"
     --contimeout "$CONTIMEOUT" --timeout "$TIMEOUT" --checksum
     --log-file "$LOGFILE" --log-level INFO --stats 5m)

do_push() {
  log "PUSH 本地 -> 远端: $LOCAL_DIR  =>  $REMOTE_PATH"
  "$RCLONE" sync "$LOCAL_DIR" "$REMOTE_PATH" \
    --backup-dir "$TRASH_REMOTE" \
    --max-delete "$MAX_DELETE" "${NET[@]}"
  return $?
}
do_pull() {
  log "PULL 远端共享 -> 本地缓存: $SHARED_REMOTE  =>  $CACHE_DIR"
  "$RCLONE" copy "$SHARED_REMOTE" "$CACHE_DIR" "${NET[@]}"
  return $?
}

rc=0
case "$MODE" in
  push) do_push || rc=$? ;;
  pull) do_pull || rc=$? ;;
  both) do_push || rc=$?; do_pull || rc=$? ;;
  *) echo "用法: $0 [both|push|pull]"; exit 64 ;;
esac
if [ "$rc" -eq 0 ]; then log "OK"; else log "FAIL rc=$rc（下次重试；详见 $LOGFILE）"; fi
exit "$rc"


