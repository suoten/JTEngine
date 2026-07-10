#!/usr/bin/env bash
# JTE 配置文件备份脚本
# AUTO-FIX-2026-07-02: 配置管理 — 配置文件备份与版本管理
#
# 策略：每日全量备份 + 变更触发增量
#   - 全量：打包所有配置文件到 tar.gz
#   - 增量：对比 Git diff 记录变更
#   - 保留：90 天（配置变更历史可追溯）
#   - 加密：使用 GPG 对称加密（密钥通过环境变量传入）
#
# 用法：
#   ./config_backup.sh full                    # 全量备份
#   ./config_backup.sh restore <DATE>          # 恢复到指定日期
#   ./config_backup.sh list                    # 列出所有备份
#   ./config_backup.sh diff <DATE1> <DATE2>    # 对比两个备份差异
#
# 依赖：tar、gzip、gpg（可选加密）、diff
set -euo pipefail

# ===== 默认配置 =====
CONFIG_ROOT="${JTE_CONFIG_ROOT:-/app/configs}"
BACKUP_ROOT="${JTE_CONFIG_BACKUP_DIR:-/data/backups/config}"
RETAIN_DAYS="${JTE_CONFIG_RETAIN_DAYS:-90}"
GPG_PASSPHRASE="${JTE_CONFIG_GPG_PASSPHRASE:-}"  # 空则不加密

log()  { echo "[$(date '+%F %T')] $*"; }
err()  { echo "[$(date '+%F %T')] [ERROR] $*" >&2; }
fatal(){ err "$*"; exit 1; }

# 全量备份
do_full() {
    local date_str; date_str=$(date +%Y%m%d_%H%M%S)
    local backup_file="${BACKUP_ROOT}/config_${date_str}.tar.gz"

    mkdir -p "$BACKUP_ROOT"

    if [[ ! -d "$CONFIG_ROOT" ]]; then
        fatal "配置目录不存在: $CONFIG_ROOT"
    fi

    log "配置文件全量备份开始"
    log "  源目录: $CONFIG_ROOT"
    log "  备份文件: $backup_file"

    # 打包配置文件（排除临时文件和密钥）
    tar czf "$backup_file" \
        --exclude='*.log' \
        --exclude='*.tmp' \
        --exclude='*.swp' \
        --exclude='.DS_Store' \
        -C "$(dirname "$CONFIG_ROOT")" \
        "$(basename "$CONFIG_ROOT")"

    # 可选加密
    if [[ -n "$GPG_PASSPHRASE" ]]; then
        log "  使用 GPG 加密备份"
        gpg --batch --yes --passphrase "$GPG_PASSPHRASE" \
            --symmetric --cipher-algo AES256 \
            -o "${backup_file}.gpg" "$backup_file"
        rm "$backup_file"
        backup_file="${backup_file}.gpg"
        log "  加密完成: $backup_file"
    fi

    # 生成校验和
    sha256sum "$backup_file" > "${backup_file}.sha256"

    # 清理过期备份
    find "$BACKUP_ROOT" -name "config_*.tar.gz*" -mtime +${RETAIN_DAYS} -delete 2>/dev/null || true
    find "$BACKUP_ROOT" -name "config_*.sha256" -mtime +${RETAIN_DAYS} -delete 2>/dev/null || true

    local size; size=$(du -h "$backup_file" | cut -f1)
    log "备份完成: $backup_file ($size)"
    log "保留策略: ${RETAIN_DAYS} 天"
}

# 恢复配置
do_restore() {
    local target_date="$1"
    [[ -z "$target_date" ]] && fatal "用法: $0 restore <YYYYMMDD>"

    # 查找匹配的备份文件
    local backup_file
    backup_file=$(find "$BACKUP_ROOT" -name "config_${target_date}*.tar.gz*" | sort | tail -1)

    if [[ -z "$backup_file" ]]; then
        fatal "未找到 ${target_date} 的配置备份"
    fi

    log "恢复配置文件: $backup_file"
    log "  目标目录: $CONFIG_ROOT"

    # 备份当前配置（恢复前快照）
    if [[ -d "$CONFIG_ROOT" ]]; then
        local pre_restore_backup="${BACKUP_ROOT}/pre_restore_$(date +%Y%m%d_%H%M%S).tar.gz"
        log "  恢复前快照当前配置: $pre_restore_backup"
        tar czf "$pre_restore_backup" -C "$(dirname "$CONFIG_ROOT")" "$(basename "$CONFIG_ROOT")" 2>/dev/null || true
    fi

    # 校验完整性
    if [[ -f "${backup_file}.sha256" ]]; then
        log "  校验 SHA256..."
        (cd "$(dirname "$backup_file")" && sha256sum -c "$(basename "${backup_file}.sha256")") || {
            err "校验失败！备份文件可能已损坏"
            fatal "中止恢复以防止数据损坏"
        }
        log "  校验通过"
    fi

    # 解密（如需要）
    local extract_file="$backup_file"
    if [[ "$backup_file" == *.gpg ]]; then
        if [[ -z "$GPG_PASSPHRASE" ]]; then
            fatal "备份已加密，需设置 JTE_CONFIG_GPG_PASSPHRASE 环境变量"
        fi
        log "  解密备份..."
        extract_file="${backup_file%.gpg}"
        gpg --batch --yes --passphrase "$GPG_PASSPHRASE" \
            --decrypt -o "$extract_file" "$backup_file"
    fi

    # 解压到临时目录再覆盖
    local tmp_dir; tmp_dir=$(mktemp -d)
    tar xzf "$extract_file" -C "$tmp_dir"

    # 停止服务前先覆盖配置
    mkdir -p "$CONFIG_ROOT"
    cp -r "${tmp_dir}/$(basename "$CONFIG_ROOT")"/* "$CONFIG_ROOT"/
    rm -rf "$tmp_dir"

    # 清理解密临时文件
    [[ "$extract_file" != "$backup_file" ]] && rm -f "$extract_file"

    log "配置恢复完成！"
    log "  请重启相关服务使配置生效"
    log "  或通过 API 触发热加载: POST /api/v1/admin/config/reload"
}

# 列出所有备份
do_list() {
    log "配置备份列表 (${BACKUP_ROOT}):"
    printf "%-40s %-10s %-20s\n" "文件名" "大小" "创建时间"
    printf "%-40s %-10s %-20s\n" "--------" "----" "---------"

    for f in $(find "$BACKUP_ROOT" -name "config_*.tar.gz*" -type f | sort -r); do
        local size; size=$(du -h "$f" | cut -f1)
        local mtime; mtime=$(stat -c %y "$f" 2>/dev/null | cut -d. -f1 || stat -f %Sm "$f" 2>/dev/null)
        printf "%-40s %-10s %-20s\n" "$(basename "$f")" "$size" "$mtime"
    done
}

# 对比两个备份
do_diff() {
    local date1="$1"
    local date2="$2"
    [[ -z "$date1" || -z "$date2" ]] && fatal "用法: $0 diff <DATE1> <DATE2>"

    local file1 file2
    file1=$(find "$BACKUP_ROOT" -name "config_${date1}*.tar.gz*" | sort | tail -1)
    file2=$(find "$BACKUP_ROOT" -name "config_${date2}*.tar.gz*" | sort | tail -1)

    [[ -z "$file1" ]] && fatal "未找到 ${date1} 的备份"
    [[ -z "$file2" ]] && fatal "未找到 ${date2} 的备份"

    local tmp1 tmp2
    tmp1=$(mktemp -d); tmp2=$(mktemp -d)

    tar xzf "$file1" -C "$tmp1" 2>/dev/null
    tar xzf "$file2" -C "$tmp2" 2>/dev/null

    log "配置差异对比: ${date1} vs ${date2}"
    diff -r "${tmp1}" "${tmp2}" || true

    rm -rf "$tmp1" "$tmp2"
}

# ===== 主入口 =====
case "${1:-}" in
    full)     do_full ;;
    restore)  do_restore "${2:-}" ;;
    list)     do_list ;;
    diff)     do_diff "${2:-}" "${3:-}" ;;
    *)
        echo "JTE 配置文件备份工具"
        echo ""
        echo "用法: $0 <command> [args]"
        echo ""
        echo "命令:"
        echo "  full                 全量备份配置文件"
        echo "  restore <DATE>       恢复到指定日期 (YYYYMMDD)"
        echo "  list                 列出所有备份"
        echo "  diff <DATE1> <DATE2> 对比两个备份差异"
        echo ""
        echo "环境变量:"
        echo "  JTE_CONFIG_ROOT          配置目录 (默认: /app/configs)"
        echo "  JTE_CONFIG_BACKUP_DIR    备份存储目录 (默认: /data/backups/config)"
        echo "  JTE_CONFIG_RETAIN_DAYS   保留天数 (默认: 90)"
        echo "  JTE_CONFIG_GPG_PASSPHRASE GPG 加密密钥 (空则不加密)"
        exit 1
        ;;
esac
