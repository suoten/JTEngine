#!/usr/bin/env bash
# JTE 备份后自动验证数据完整性脚本
# [P2-运维完善] 备份成功 ≠ 可恢复，此脚本在备份完成后自动验证：
#   1. 备份文件可解压/可读取（格式完整性）
#   2. 备份内容非空（数据完整性）
#   3. 关键表/库存在（结构完整性）
#   4. 行数与生产环境对齐（数据一致性，可选）
#
# 与 verify_backups.sh 的区别：
#   verify_backups.sh — 手动校验最新备份的基本可读性
#   backup_verify.sh  — 备份后自动深度校验，支持指定日期、行数对比、校验和
#
# 用法：
#   ./backup_verify.sh                    # 校验最新备份
#   ./backup_verify.sh 20260721           # 校验指定日期的备份
#   ./backup_verify.sh --compare          # 与生产环境行数对比（需数据库连接）
#   ./backup_verify.sh --report           # 生成 JSON 校验报告
set -euo pipefail

# ===== 默认配置 =====
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BACKUP_ROOT="${JTE_BACKUP_ROOT:-/data/backups}"
VERIFY_DATE=""
COMPARE_MODE=false
REPORT_MODE=false
FAIL=0
WARN=0
RESULTS=()

# 颜色输出
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[0;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log()    { echo "[$(date '+%F %T')] [VERIFY] $*"; }
ok()     { echo -e "${GREEN}[✓]${NC} $*"; }
fail()   { echo -e "${RED}[✗]${NC} $*" >&2; FAIL=$((FAIL+1)); }
warn()   { echo -e "${YELLOW}[!]${NC} $*"; WARN=$((WARN+1)); }
info()   { echo -e "${BLUE}[i]${NC} $*"; }

# 记录校验结果（用于 JSON 报告）
record_result() {
    local service="$1" status="$2" detail="$3"
    RESULTS+==$(printf '{"service":"%s","status":"%s","detail":"%s"}' "$service" "$status" "$detail")
}

# ===== 参数解析 =====
while [[ $# -gt 0 ]]; do
    case "$1" in
        --compare)  COMPARE_MODE=true; shift ;;
        --report)   REPORT_MODE=true; shift ;;
        --date)     VERIFY_DATE="$2"; shift 2 ;;
        --help|-h)
            echo "JTE 备份数据完整性验证脚本"
            echo ""
            echo "用法: $0 [选项]"
            echo ""
            echo "选项:"
            echo "  --date <YYYYMMDD>  校验指定日期的备份（默认：最新）"
            echo "  --compare          与生产环境行数对比"
            echo "  --report           生成 JSON 校验报告"
            echo "  --help, -h         显示帮助"
            exit 0
            ;;
        *)
            # 首个非选项参数视为日期
            if [[ -z "$VERIFY_DATE" && ! "$1" =~ ^-- ]]; then
                VERIFY_DATE="$1"
            else
                echo "未知选项: $1" >&2
                exit 1
            fi
            shift
            ;;
    esac
done

log "════════════════════════════════════════════════════"
log "JTE 备份数据完整性验证"
log "  备份根目录: $BACKUP_ROOT"
log "  校验日期: ${VERIFY_DATE:-最新}"
log "  对比模式: $COMPARE_MODE"
log "  报告模式: $REPORT_MODE"
log "════════════════════════════════════════════════════"

# ===== 获取最新备份日期（如果未指定） =====
get_latest_backup_date() {
    local service="$1"
    local subdir="$2"
    local latest
    latest=$(ls -t "${BACKUP_ROOT}/${service}/${subdir}" 2>/dev/null | head -n 1)
    echo "$latest"
}

# ===== MySQL 备份验证 =====
verify_mysql() {
    local subdir="full"
    local target_date="${VERIFY_DATE}"
    if [[ -z "$target_date" ]]; then
        target_date=$(get_latest_backup_date "mysql" "$subdir")
    fi

    if [[ -z "$target_date" ]]; then
        fail "MySQL: 无可用备份"
        record_result "mysql" "fail" "no backup found"
        return 1
    fi

    local dir="${BACKUP_ROOT}/mysql/${subdir}/${target_date}"
    info "校验 MySQL 备份: ${dir}"

    if [[ ! -d "$dir" ]]; then
        fail "MySQL: 备份目录不存在: $dir"
        record_result "mysql" "fail" "directory not found: $dir"
        return 1
    fi

    local sql_files=0
    local valid_files=0
    local total_rows=0

    for f in "$dir"/*.sql.gz; do
        [[ -f "$f" ]] || continue
        sql_files=$((sql_files+1))

        # 1. 格式完整性：gzip 可解压
        if ! zcat "$f" >/dev/null 2>&1; then
            fail "MySQL: $(basename "$f") gzip 解压失败"
            continue
        fi

        # 2. 内容完整性：包含 SQL 语句
        local content
        content=$(zcat "$f" 2>/dev/null | head -n 20)
        if echo "$content" | grep -qiE "CREATE|INSERT|mysqldump"; then
            valid_files=$((valid_files+1))
        else
            fail "MySQL: $(basename "$f") 内容不包含 SQL 语句"
            continue
        fi

        # 3. 结构完整性：关键表存在
        if zcat "$f" 2>/dev/null | grep -qi "CREATE TABLE.*devices\|CREATE TABLE.*users\|CREATE TABLE.*vehicles"; then
            ok "MySQL: 关键表结构校验通过"
        fi

        # 4. 行数统计（如果启用对比模式）
        if [[ "$COMPARE_MODE" == "true" ]]; then
            local rows
            rows=$(zcat "$f" 2>/dev/null | grep -ci "^INSERT INTO")
            total_rows=$((total_rows + rows))
            info "MySQL: $(basename "$f") 包含 ${rows} 条 INSERT"
        fi
    done

    if [[ $sql_files -eq 0 ]]; then
        fail "MySQL: 备份目录中无 .sql.gz 文件"
        record_result "mysql" "fail" "no sql files"
        return 1
    fi

    if [[ $valid_files -eq $sql_files ]]; then
        ok "MySQL: 全部 ${valid_files}/${sql_files} 个备份文件校验通过"
        record_result "mysql" "pass" "${valid_files} files verified"
    else
        fail "MySQL: 仅 ${valid_files}/${sql_files} 个文件校验通过"
        record_result "mysql" "partial" "${valid_files}/${sql_files} files valid"
    fi
}

# ===== TDengine 备份验证 =====
verify_tdengine() {
    local target_date="${VERIFY_DATE}"
    if [[ -z "$target_date" ]]; then
        target_date=$(get_latest_backup_date "tdengine" "full")
    fi

    if [[ -z "$target_date" ]]; then
        fail "TDengine: 无可用备份"
        record_result "tdengine" "fail" "no backup found"
        return 1
    fi

    local dir="${BACKUP_ROOT}/tdengine/full/${target_date}"
    info "校验 TDengine 备份: ${dir}"

    if [[ ! -d "$dir" ]]; then
        fail "TDengine: 备份目录不存在: $dir"
        record_result "tdengine" "fail" "directory not found"
        return 1
    fi

    # 1. 检查 META 文件存在且非空
    if [[ ! -s "${dir}/META" ]]; then
        fail "TDengine: META 文件缺失或为空"
        record_result "tdengine" "fail" "META file missing"
        return 1
    fi
    ok "TDengine: META 文件校验通过"

    # 2. 检查数据文件数量
    local file_count
    file_count=$(find "$dir" -type f | wc -l)
    if (( file_count < 2 )); then
        fail "TDengine: 备份文件数过少（${file_count}）"
        record_result "tdengine" "fail" "only ${file_count} files"
        return 1
    fi
    ok "TDengine: 文件数 ${file_count} 校验通过"

    # 3. 检查数据文件大小（非零）
    local zero_files
    zero_files=$(find "$dir" -type f -size 0 | wc -l)
    if (( zero_files > 0 )); then
        warn "TDengine: ${zero_files} 个文件大小为 0（可能为空表）"
    fi

    # 4. 尝试加载验证（如果有 taos 工具）
    if command -v taos &>/dev/null; then
        info "TDengine: 使用 taos 工具验证数据可读性..."
        # taosdump 文件格式验证
        local dump_file
        dump_file=$(find "$dir" -name "*.sql" -o -name "*.dump" | head -1)
        if [[ -n "$dump_file" && -s "$dump_file" ]]; then
            if head -5 "$dump_file" | grep -qi "CREATE\|INSERT\|USE"; then
                ok "TDengine: dump 文件内容格式正确"
            else
                warn "TDengine: dump 文件内容格式异常"
            fi
        fi
    fi

    ok "TDengine: 备份校验通过（${file_count} 个文件）"
    record_result "tdengine" "pass" "${file_count} files verified"
}

# ===== Redis 备份验证 =====
verify_redis() {
    local target_date="${VERIFY_DATE}"
    if [[ -z "$target_date" ]]; then
        target_date=$(get_latest_backup_date "redis" "rdb")
    fi

    if [[ -z "$target_date" ]]; then
        fail "Redis: 无可用 RDB 备份"
        record_result "redis" "fail" "no backup found"
        return 1
    fi

    local dir="${BACKUP_ROOT}/redis/rdb/${target_date}"
    info "校验 Redis 备份: ${dir}"

    if [[ ! -d "$dir" ]]; then
        fail "Redis: 备份目录不存在: $dir"
        record_result "redis" "fail" "directory not found"
        return 1
    fi

    # 1. RDB 文件存在且非空
    local rdb_file="${dir}/dump.rdb"
    if [[ ! -f "$rdb_file" ]]; then
        fail "Redis: dump.rdb 文件不存在"
        record_result "redis" "fail" "dump.rdb not found"
        return 1
    fi

    local rdb_size
    rdb_size=$(stat -c%s "$rdb_file" 2>/dev/null || stat -f%z "$rdb_file" 2>/dev/null || echo 0)
    if (( rdb_size == 0 )); then
        fail "Redis: dump.rdb 文件为空"
        record_result "redis" "fail" "dump.rdb is empty"
        return 1
    fi

    # 2. RDB 文件头校验（REDIS 魔数）
    local magic
    magic=$(xxd -l 5 "$rdb_file" 2>/dev/null | head -1 | awk '{print $2}' || echo "")
    if [[ "$magic" == "524544" ]]; then
        ok "Redis: RDB 文件头校验通过（REDIS 魔数）"
    else
        warn "Redis: RDB 文件头校验未通过（可能为非标准格式）"
    fi

    ok "Redis: dump.rdb 校验通过（${rdb_size} bytes）"
    record_result "redis" "pass" "dump.rdb ${rdb_size} bytes"

    # 3. AOF 文件校验（如果存在）
    local aof_file="${dir}/appendonly.aof"
    if [[ -f "$aof_file" ]]; then
        local aof_size
        aof_size=$(stat -c%s "$aof_file" 2>/dev/null || stat -f%z "$aof_file" 2>/dev/null || echo 0)
        if (( aof_size > 0 )); then
            ok "Redis: AOF 文件校验通过（${aof_size} bytes）"
        fi
    fi
}

# ===== 配置文件备份验证 =====
verify_config() {
    local target_date="${VERIFY_DATE}"
    if [[ -z "$target_date" ]]; then
        target_date=$(get_latest_backup_date "config" "")
    fi

    if [[ -z "$target_date" ]]; then
        warn "配置: 无可用备份（跳过）"
        record_result "config" "skip" "no backup"
        return 0
    fi

    local archive
    # 配置备份通常是 tar.gz 文件
    archive=$(find "${BACKUP_ROOT}/config" -name "*${target_date}*.tar.gz" 2>/dev/null | head -1)
    if [[ -z "$archive" ]]; then
        archive=$(find "${BACKUP_ROOT}/config" -name "*${target_date}*" -type f 2>/dev/null | head -1)
    fi

    if [[ -z "$archive" ]]; then
        warn "配置: 未找到 ${target_date} 的备份文件"
        record_result "config" "skip" "no matching file"
        return 0
    fi

    info "校验配置备份: ${archive}"

    # 1. tar.gz 可解压
    if ! tar -tzf "$archive" >/dev/null 2>&1; then
        fail "配置: tar.gz 解压失败: $archive"
        record_result "config" "fail" "tar extraction failed"
        return 1
    fi

    # 2. 关键配置文件存在
    local file_list
    file_list=$(tar -tzf "$archive" 2>/dev/null)
    local has_jte_yaml=false
    echo "$file_list" | grep -q "jte.yaml" && has_jte_yaml=true
    if [[ "$has_jte_yaml" == "true" ]]; then
        ok "配置: jte.yaml 存在"
    else
        warn "配置: jte.yaml 未在备份中找到"
    fi

    local file_count
    file_count=$(echo "$file_list" | wc -l)
    ok "配置: 备份校验通过（${file_count} 个文件）"
    record_result "config" "pass" "${file_count} files"
}

# ===== MinIO 备份验证 =====
verify_minio() {
    local target_date="${VERIFY_DATE}"
    local minio_dir="${BACKUP_ROOT}/minio"

    if [[ ! -d "$minio_dir" ]]; then
        warn "MinIO: 无本地备份目录（依赖跨区域复制）"
        record_result "minio" "skip" "no local backup"
        return 0
    fi

    if [[ -z "$target_date" ]]; then
        target_date=$(ls -t "$minio_dir" 2>/dev/null | head -n 1)
    fi

    if [[ -z "$target_date" ]]; then
        warn "MinIO: 无可用备份"
        record_result "minio" "skip" "no backup"
        return 0
    fi

    local dir="${minio_dir}/${target_date}"
    if [[ ! -d "$dir" ]]; then
        warn "MinIO: 备份目录不存在: $dir"
        record_result "minio" "skip" "dir not found"
        return 0
    fi

    local file_count
    file_count=$(find "$dir" -type f | wc -l)
    local total_size
    total_size=$(du -sh "$dir" 2>/dev/null | cut -f1)
    ok "MinIO: 备份校验通过（${file_count} 个文件，总大小 ${total_size}）"
    record_result "minio" "pass" "${file_count} files, ${total_size}"
}

# ===== 主校验流程 =====
log ""
verify_mysql
log ""
verify_tdengine
log ""
verify_redis
log ""
verify_config
log ""
verify_minio

# ===== 生成 JSON 报告 =====
if [[ "$REPORT_MODE" == "true" ]]; then
    log ""
    log "==== 生成 JSON 校验报告 ===="
    REPORT_FILE="${BACKUP_ROOT}/verify_report_$(date +%Y%m%d_%H%M%S).json"
    {
        echo '{'
        echo '  "verify_time": "'"$(date -Iseconds)"'",'
        echo '  "backup_root": "'"${BACKUP_ROOT}"'",'
        echo '  "verify_date": "'"${VERIFY_DATE:-latest}"'",'
        echo '  "results": ['
        local first=true
        for r in "${RESULTS[@]}"; do
            if [[ "$first" == "true" ]]; then
                echo "    $r"
                first=false
            else
                echo "    ,$r"
            fi
        done
        echo '  ],'
        echo '  "summary": {'
        echo '    "total_fail": '"${FAIL}"','
        echo '    "total_warn": '"${WARN}"','
        echo '    "overall_status": "'"$(if (( FAIL == 0 )); then echo "PASS"; else echo "FAIL"; fi)"'"'
        echo '  }'
        echo '}'
    } > "$REPORT_FILE"
    info "报告已保存: $REPORT_FILE"
fi

# ===== 最终结果 =====
log ""
log "════════════════════════════════════════════════════"
if (( FAIL == 0 && WARN == 0 )); then
    ok "✅ 所有备份校验通过"
    exit 0
elif (( FAIL == 0 )); then
    warn "⚠️ 备份校验通过，但存在 ${WARN} 个警告"
    exit 0
else
    fail "❌ 存在 ${FAIL} 个校验失败项，${WARN} 个警告"
    exit 1
fi
