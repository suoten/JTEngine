package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/suoten/jt-engine/pkg/storage"
	"github.com/suoten/jt-engine/pkg/storage/safesql"
)

// migrateGeofence 创建电子围栏表（在 migrate 之后调用）
func (s *SQLiteStore) migrateGeofence() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS geofences (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL DEFAULT '',
		type INTEGER NOT NULL DEFAULT 1,
		org_id TEXT DEFAULT '',
		params TEXT DEFAULT '{}',
		start_time DATETIME,
		end_time DATETIME,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	)`)
	if err != nil {
		return fmt.Errorf("create geofences table: %w", err)
	}
	_, err = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_geofences_org_id ON geofences(org_id)`)
	return err
}

// QueryDrivers 查询驾驶员信息列表
func (s *SQLiteStore) QueryDrivers(ctx context.Context, opts storage.ListOptions) (*storage.ListResult, error) {
	where := []string{"1=1"}
	args := []interface{}{}
	if opts.Phone != "" {
		// FIXED: [LIKE 通配符注入] 使用 SanitizeLikeValue 转义用户输入中的 % 和 _ [2026-07-17]
		where = append(where, "phone LIKE ? ESCAPE '\\'")
		args = append(args, safesql.SanitizeLikeValue(opts.Phone)+"%")
	}
	whereClause := strings.Join(where, " AND ")

	var total int64
	countSQL := fmt.Sprintf("SELECT COUNT(*) FROM driver_info WHERE %s", whereClause)
	if err := s.db.QueryRowContext(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, err
	}

	offset := opts.Offset
	if offset == 0 && opts.Page > 0 {
		offset = (opts.Page - 1) * opts.PageSize
	}
	pageSize := opts.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}

	querySQL := fmt.Sprintf("SELECT id, vehicle_id, phone, driver_name, license_id, license_org, license_expiry, id_card, received_at, source FROM driver_info WHERE %s ORDER BY received_at DESC LIMIT ? OFFSET ?", whereClause)
	queryArgs := append(args, pageSize, offset)
	rows, err := s.db.QueryContext(ctx, querySQL, queryArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]*storage.DriverInfoData, 0)
	for rows.Next() {
		var d storage.DriverInfoData
		if err := rows.Scan(&d.ID, &d.VehicleID, &d.Phone, &d.DriverName, &d.LicenseID, &d.LicenseOrg, &d.LicenseExpiry, &d.IDCard, &d.ReceivedAt, &d.Source); err != nil {
			return nil, err
		}
		items = append(items, &d)
	}
	return &storage.ListResult{Items: items, Total: total, Page: opts.Page, Size: pageSize}, nil
}

// DeleteDriver 删除驾驶员信息
func (s *SQLiteStore) DeleteDriver(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM driver_info WHERE id = ?", id)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("driver not found: %s", id)
	}
	return nil
}

// SaveGeofence 保存电子围栏
func (s *SQLiteStore) SaveGeofence(ctx context.Context, g *storage.Geofence) error {
	if g.CreatedAt.IsZero() {
		g.CreatedAt = time.Now()
	}
	g.UpdatedAt = time.Now()
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO geofences (id, name, type, org_id, params, start_time, end_time, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		g.ID, g.Name, g.Type, g.OrgID, g.Params, g.StartTime, g.EndTime, g.CreatedAt, g.UpdatedAt)
	return err
}

// GetGeofence 查询单个电子围栏
func (s *SQLiteStore) GetGeofence(ctx context.Context, id string) (*storage.Geofence, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, name, type, org_id, params, start_time, end_time, created_at, updated_at FROM geofences WHERE id = ?`, id)
	var g storage.Geofence
	var startTime, endTime, createdAt, updatedAt sql.NullTime
	if err := row.Scan(&g.ID, &g.Name, &g.Type, &g.OrgID, &g.Params, &startTime, &endTime, &createdAt, &updatedAt); err != nil {
		return nil, fmt.Errorf("geofence not found: %s", id)
	}
	g.StartTime = startTime.Time
	g.EndTime = endTime.Time
	g.CreatedAt = createdAt.Time
	g.UpdatedAt = updatedAt.Time
	return &g, nil
}

// ListGeofences 查询电子围栏列表
func (s *SQLiteStore) ListGeofences(ctx context.Context, opts storage.ListOptions) (*storage.ListResult, error) {
	where := []string{"1=1"}
	args := []interface{}{}
	if opts.OrgID != "" {
		where = append(where, "org_id = ?")
		args = append(args, opts.OrgID)
	}
	whereClause := strings.Join(where, " AND ")

	var total int64
	countSQL := fmt.Sprintf("SELECT COUNT(*) FROM geofences WHERE %s", whereClause)
	if err := s.db.QueryRowContext(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, err
	}

	offset := opts.Offset
	if offset == 0 && opts.Page > 0 {
		offset = (opts.Page - 1) * opts.PageSize
	}
	pageSize := opts.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}

	querySQL := fmt.Sprintf("SELECT id, name, type, org_id, params, start_time, end_time, created_at, updated_at FROM geofences WHERE %s ORDER BY created_at DESC LIMIT ? OFFSET ?", whereClause)
	queryArgs := append(args, pageSize, offset)
	rows, err := s.db.QueryContext(ctx, querySQL, queryArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]*storage.Geofence, 0)
	for rows.Next() {
		var g storage.Geofence
		var startTime, endTime, createdAt, updatedAt sql.NullTime
		if err := rows.Scan(&g.ID, &g.Name, &g.Type, &g.OrgID, &g.Params, &startTime, &endTime, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		g.StartTime = startTime.Time
		g.EndTime = endTime.Time
		g.CreatedAt = createdAt.Time
		g.UpdatedAt = updatedAt.Time
		items = append(items, &g)
	}
	return &storage.ListResult{Items: items, Total: total, Page: opts.Page, Size: pageSize}, nil
}

// DeleteGeofence 删除电子围栏
func (s *SQLiteStore) DeleteGeofence(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM geofences WHERE id = ?", id)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("geofence not found: %s", id)
	}
	return nil
}

// AUTO-FIX-2026-06-29 [P1-6]: 809 转发规则持久化（关系库 CRUD）
// 原转发规则仅 YAML 静态配置，运行期无法修改且无报警类型/级别/时间过滤。
// 新增 forward_rules 表支持热更新：JT809Client 启动时加载、API 变更后刷新内存快照。

// migrateForwardRule 创建 809 转发规则表。
func (s *SQLiteStore) migrateForwardRule() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS forward_rules (
		id TEXT PRIMARY KEY,
		platform_id TEXT NOT NULL DEFAULT '',
		data_type TEXT NOT NULL DEFAULT '',
		phone TEXT DEFAULT '',
		alarm_types TEXT DEFAULT '',
		min_level INTEGER DEFAULT 0,
		time_start TEXT DEFAULT '',
		time_end TEXT DEFAULT '',
		enabled INTEGER DEFAULT 1,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	)`)
	if err != nil {
		return fmt.Errorf("create forward_rules table: %w", err)
	}
	// AUTO-FIX-2026-07-02: 新增 source_platform_id 列（兼容已有库，ALTER TABLE 幂等）
	_, _ = s.db.Exec(`ALTER TABLE forward_rules ADD COLUMN source_platform_id TEXT DEFAULT ''`)
	_, err = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_forward_rules_platform ON forward_rules(platform_id)`)
	if err != nil {
		return fmt.Errorf("create forward_rules platform index: %w", err)
	}
	_, err = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_forward_rules_data_type ON forward_rules(data_type)`)
	if err != nil {
		return fmt.Errorf("create forward_rules data_type index: %w", err)
	}
	_, err = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_forward_rules_source ON forward_rules(source_platform_id)`)
	return err
}

// SaveForwardRule 保存或更新转发规则（INSERT OR REPLACE 语义）。
func (s *SQLiteStore) SaveForwardRule(ctx context.Context, rule *storage.ForwardRule) error {
	if rule.CreatedAt.IsZero() {
		rule.CreatedAt = time.Now()
	}
	rule.UpdatedAt = time.Now()
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO forward_rules (id, source_platform_id, platform_id, data_type, phone, alarm_types, min_level, time_start, time_end, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rule.ID, rule.SourcePlatformID, rule.PlatformID, rule.DataType, rule.Phone, rule.AlarmTypes,
		rule.MinLevel, rule.TimeStart, rule.TimeEnd, boolToInt(rule.Enabled),
		rule.CreatedAt, rule.UpdatedAt)
	return err
}

// GetForwardRule 按主键查询单条转发规则。
func (s *SQLiteStore) GetForwardRule(ctx context.Context, id string) (*storage.ForwardRule, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, source_platform_id, platform_id, data_type, phone, alarm_types, min_level, time_start, time_end, enabled, created_at, updated_at
		 FROM forward_rules WHERE id = ?`, id)
	r := &storage.ForwardRule{}
	var enabled int
	var createdAt, updatedAt sql.NullTime
	if err := row.Scan(&r.ID, &r.SourcePlatformID, &r.PlatformID, &r.DataType, &r.Phone, &r.AlarmTypes,
		&r.MinLevel, &r.TimeStart, &r.TimeEnd, &enabled, &createdAt, &updatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("forward rule not found: %s", id)
		}
		return nil, err
	}
	r.Enabled = enabled == 1
	if createdAt.Valid {
		r.CreatedAt = createdAt.Time
	}
	if updatedAt.Valid {
		r.UpdatedAt = updatedAt.Time
	}
	return r, nil
}

// ListForwardRules 按上级平台 ID 查询启用的转发规则列表。
// platformID 为空时返回全部规则（用于管理后台审计）。
func (s *SQLiteStore) ListForwardRules(ctx context.Context, platformID string) ([]*storage.ForwardRule, error) {
	query := `SELECT id, source_platform_id, platform_id, data_type, phone, alarm_types, min_level, time_start, time_end, enabled, created_at, updated_at FROM forward_rules`
	args := []interface{}{}
	if platformID != "" {
		query += ` WHERE platform_id = ?`
		args = append(args, platformID)
	}
	query += ` ORDER BY created_at ASC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]*storage.ForwardRule, 0)
	for rows.Next() {
		r := &storage.ForwardRule{}
		var enabled int
		var createdAt, updatedAt sql.NullTime
		if err := rows.Scan(&r.ID, &r.SourcePlatformID, &r.PlatformID, &r.DataType, &r.Phone, &r.AlarmTypes,
			&r.MinLevel, &r.TimeStart, &r.TimeEnd, &enabled, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		r.Enabled = enabled == 1
		if createdAt.Valid {
			r.CreatedAt = createdAt.Time
		}
		if updatedAt.Valid {
			r.UpdatedAt = updatedAt.Time
		}
		items = append(items, r)
	}
	return items, rows.Err()
}

// DeleteForwardRule 按主键删除转发规则，不存在时返回错误。
func (s *SQLiteStore) DeleteForwardRule(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM forward_rules WHERE id = ?", id)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("forward rule not found: %s", id)
	}
	return nil
}

// AUTO-FIX-2026-07-02 [P1]: 809 级联平台配置持久化（关系库 CRUD）
// 替代 YAML-only 的 DownstreamPlatformConfig/JT809PlatformConfig，
// 支持运行时动态增删上下级平台，无需重启服务。

// migratePlatform 创建 809 级联平台配置表。
func (s *SQLiteStore) migratePlatform() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS platforms (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL DEFAULT '',
		user_id TEXT NOT NULL DEFAULT '',
		password TEXT NOT NULL DEFAULT '',
		role TEXT NOT NULL DEFAULT 'downstream',
		host TEXT DEFAULT '',
		port INTEGER DEFAULT 0,
		link_type INTEGER DEFAULT 0,
		downlink_id TEXT DEFAULT '',
		enabled INTEGER DEFAULT 1,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	)`)
	if err != nil {
		return fmt.Errorf("create platforms table: %w", err)
	}
	_, err = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_platforms_role ON platforms(role)`)
	if err != nil {
		return fmt.Errorf("create platforms role index: %w", err)
	}
	_, err = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_platforms_user_id ON platforms(user_id)`)
	return err
}

// SavePlatform 保存或更新平台配置（INSERT OR REPLACE 语义）。
func (s *SQLiteStore) SavePlatform(ctx context.Context, p *storage.Platform) error {
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now()
	}
	p.UpdatedAt = time.Now()
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO platforms (id, name, user_id, password, role, host, port, link_type, downlink_id, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Name, p.UserID, p.Password, p.Role, p.Host, p.Port, p.LinkType, p.DownLinkID,
		boolToInt(p.Enabled), p.CreatedAt, p.UpdatedAt)
	return err
}

// GetPlatform 按主键查询单条平台配置。
func (s *SQLiteStore) GetPlatform(ctx context.Context, id string) (*storage.Platform, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, name, user_id, password, role, host, port, link_type, downlink_id, enabled, created_at, updated_at
		 FROM platforms WHERE id = ?`, id)
	p := &storage.Platform{}
	var enabled int
	var createdAt, updatedAt sql.NullTime
	if err := row.Scan(&p.ID, &p.Name, &p.UserID, &p.Password, &p.Role, &p.Host, &p.Port,
		&p.LinkType, &p.DownLinkID, &enabled, &createdAt, &updatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("platform not found: %s", id)
		}
		return nil, err
	}
	p.Enabled = enabled == 1
	if createdAt.Valid {
		p.CreatedAt = createdAt.Time
	}
	if updatedAt.Valid {
		p.UpdatedAt = updatedAt.Time
	}
	return p, nil
}

// ListPlatforms 按角色查询平台配置列表。
// role 为空时返回全部平台（"downstream"=下级平台，"upstream"=上级平台）。
func (s *SQLiteStore) ListPlatforms(ctx context.Context, role string) ([]*storage.Platform, error) {
	query := `SELECT id, name, user_id, password, role, host, port, link_type, downlink_id, enabled, created_at, updated_at FROM platforms`
	args := []interface{}{}
	if role != "" {
		query += ` WHERE role = ?`
		args = append(args, role)
	}
	query += ` ORDER BY created_at ASC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]*storage.Platform, 0)
	for rows.Next() {
		p := &storage.Platform{}
		var enabled int
		var createdAt, updatedAt sql.NullTime
		if err := rows.Scan(&p.ID, &p.Name, &p.UserID, &p.Password, &p.Role, &p.Host, &p.Port,
			&p.LinkType, &p.DownLinkID, &enabled, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		p.Enabled = enabled == 1
		if createdAt.Valid {
			p.CreatedAt = createdAt.Time
		}
		if updatedAt.Valid {
			p.UpdatedAt = updatedAt.Time
		}
		items = append(items, p)
	}
	return items, rows.Err()
}

// DeletePlatform 按主键删除平台配置，不存在时返回错误。
func (s *SQLiteStore) DeletePlatform(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM platforms WHERE id = ?", id)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("platform not found: %s", id)
	}
	return nil
}
