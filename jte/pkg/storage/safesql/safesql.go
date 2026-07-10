// Package safesql 提供 SQL 注入防护工具。
//
// 重点解决 OrderBy 字段的 SQL 注入风险：
// 现有代码中将 OrderBy 字段直接字符串拼接进 SQL，
// 虽然参数化查询防止了 WHERE 子句注入，但 ORDER BY 不支持参数绑定，
// 必须使用白名单校验。
package safesql

import (
	"fmt"
	"strings"
)

// AllowedOrderByColumns 全局允许的 OrderBy 列名白名单（小写）
// 这些是各业务表通用的排序列，超出此列表的列名将被拒绝
var AllowedOrderByColumns = map[string]bool{
	// 通用字段
	"id": true, "created_at": true, "updated_at": true, "deleted_at": true,
	"timestamp": true, "time": true, "received_at": true, "last_active": true,
	// 车辆/设备
	"phone": true, "vehicle_id": true, "device_id": true, "protocol": true,
	"plate": true, "license": true, "status": true, "online": true,
	"driver_name": true, "vehicle_no": true, "sim": true,
	// 报警
	"alarm_type": true, "level": true, "handled": true, "alarm_time": true,
	// 位置/轨迹
	"latitude": true, "longitude": true, "speed": true, "direction": true,
	"mileage": true,
	// 系统
	"name": true, "type": true, "category": true, "enabled": true,
	"last_login_at": true, "username": true, "role": true,
}

// ValidateOrderBy 校验 OrderBy 字段是否安全（白名单 + 方向校验）
// 输入格式: "column" 或 "column DESC" / "column ASC"
// 返回安全的 OrderBy 子句，或不安全时返回默认值。
//
// 用法：
//
//	safe := safesql.ValidateOrderBy(req.OrderBy, "created_at DESC")
//	query := "SELECT * FROM t ORDER BY " + safe
func ValidateOrderBy(orderBy, defaultOrder string) string {
	if orderBy == "" {
		return defaultOrder
	}

	// 拆分列名和方向
	parts := strings.Fields(strings.TrimSpace(orderBy))
	if len(parts) == 0 {
		return defaultOrder
	}

	column := strings.ToLower(strings.TrimSpace(parts[0]))
	// 去除可能的表前缀 (table.column -> column)
	if dot := strings.Index(column, "."); dot >= 0 {
		column = column[dot+1:]
	}
	// 去除反引号
	column = strings.Trim(column, "`")

	if !AllowedOrderByColumns[column] {
		return defaultOrder
	}

	direction := "ASC"
	if len(parts) >= 2 {
		dir := strings.ToUpper(parts[1])
		if dir == "DESC" || dir == "ASC" {
			direction = dir
		}
	}

	return fmt.Sprintf("`%s` %s", column, direction)
}

// ValidateOrderByMulti 校验多列 OrderBy（逗号分隔）
// 输入: "created_at DESC, name ASC"
func ValidateOrderByMulti(orderBy, defaultOrder string) string {
	if orderBy == "" {
		return defaultOrder
	}

	parts := strings.Split(orderBy, ",")
	safeParts := make([]string, 0, len(parts))
	for _, part := range parts {
		safe := ValidateOrderBy(strings.TrimSpace(part), "")
		if safe != "" {
			safeParts = append(safeParts, safe)
		}
	}
	if len(safeParts) == 0 {
		return defaultOrder
	}
	return strings.Join(safeParts, ", ")
}

// SanitizeLikeValue 转义 LIKE 查询的输入值，防止通配符注入
// % 和 _ 是 SQL LIKE 的通配符，用户输入中的这些字符需要转义
func SanitizeLikeValue(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "%", "\\%")
	value = strings.ReplaceAll(value, "_", "\\_")
	return value
}
