# JTE 备份完整性校验脚本
# AUTO-FIX-2026-06-30 [P1-5]: 备份与灾难恢复
#
# 校验最近一份备份可解压/可读取，避免"备份成功但恢复时才发现损坏"的问题。
set -euo pipefail

BACKUP_ROOT="${JTE_BACKUP_ROOT:-/data/backups}"
FAIL=0

log()  { echo "[$(date '+%F %T')] $*"; }
err()  { echo "[$(date '+%F %T')] [ERROR] $*" >&2; FAIL=1; }

# MySQL: 校验最新全量 gzip 可解压 + 含 SQL 内容
verify_mysql() {
    local latest
    latest=$(ls -t "${BACKUP_ROOT}/mysql/full" 2>/dev/null | head -n 1)
    [[ -z "$latest" ]] && { err "MySQL 无全量备份"; return; }
    local dir="${BACKUP_ROOT}/mysql/full/${latest}"
    log "校验 MySQL 备份: ${dir}"
    for f in "$dir"/*.sql.gz; do
        [[ -f "$f" ]] || continue
        if ! zcat "$f" 2>/dev/null | head -n 5 | grep -qi "CREATE\|INSERT\|mysqldump"; then
            err "  ${f} 解压/内容校验失败"
        else
            log "  ${f} OK"
        fi
    done
}

# TDengine: 校验最新全量目录非空
verify_tdengine() {
    local latest
    latest=$(ls -t "${BACKUP_ROOT}/tdengine/full" 2>/dev/null | head -n 1)
    [[ -z "$latest" ]] && { err "TDengine 无全量备份"; return; }
    local dir="${BACKUP_ROOT}/tdengine/full/${latest}"
    log "校验 TDengine 备份: ${dir}"
    if [[ ! -s "${dir}/META" ]]; then err "  META 文件缺失"; fi
    local files; files=$(find "$dir" -type f | wc -l)
    if (( files < 2 )); then err "  备份文件数过少（${files}）"; else log "  文件数 ${files} OK"; fi
}

# Redis: 校验最新 RDB 非空
verify_redis() {
    local latest
    latest=$(ls -t "${BACKUP_ROOT}/redis/rdb" 2>/dev/null | head -n 1)
    [[ -z "$latest" ]] && { err "Redis 无 RDB 备份"; return; }
    local dir="${BACKUP_ROOT}/redis/rdb/${latest}"
    log "校验 Redis 备份: ${dir}"
    local size; size=$(stat -c%s "${dir}/dump.rdb" 2>/dev/null || stat -f%z "${dir}/dump.rdb" 2>/dev/null || echo 0)
    if (( size == 0 )); then err "  dump.rdb 为空"; else log "  dump.rdb 大小 ${size} bytes OK"; fi
}

log "==== 备份完整性校验开始 ===="
verify_mysql
verify_tdengine
verify_redis

if (( FAIL == 0 )); then
    log "✅ 所有备份校验通过"
    exit 0
else
    err "❌ 存在备份校验失败项，请检查"
    exit 1
fi
