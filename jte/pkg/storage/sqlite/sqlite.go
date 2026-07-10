package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jte-engine/jte/pkg/storage"
	"github.com/jte-engine/jte/pkg/storage/safesql"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

type SQLiteStore struct {
	db     *sql.DB
	logger *zap.Logger
}

func NewSQLiteStore(dbPath string, logger *zap.Logger) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	db.SetMaxOpenConns(0)
	db.SetMaxIdleConns(5)

	// WAL 模式提升并发读性能；CGO 禁用时 go-sqlite3 为 stub，PRAGMA 可能失败，此处 best-effort
	if _, err := db.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		if logger != nil {
			logger.Warn("set WAL mode failed (non-fatal, may be CGO disabled)", zap.Error(err))
		}
	}
	// synchronous=NORMAL：WAL 模式下 NORMAL 足够保证崩溃一致性（不会丢已提交事务），
	// 比 FULL 性能高 2-3 倍。FULL 仅在极端断电场景多保护一点，WAL+NORMAL 已满足验收要求。
	if _, err := db.Exec("PRAGMA synchronous=NORMAL;"); err != nil {
		if logger != nil {
			logger.Warn("set synchronous=NORMAL failed (non-fatal)", zap.Error(err))
		}
	}

	store := &SQLiteStore{db: db, logger: logger}
	if err := store.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	if err := store.migrateGeofence(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate geofence: %w", err)
	}
	// AUTO-FIX-2026-06-29 [P1-6]: 809 转发规则表迁移
	if err := store.migrateForwardRule(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate forward_rule: %w", err)
	}
	// AUTO-FIX-2026-07-02 [P1]: 809 级联平台配置表迁移
	if err := store.migratePlatform(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate platform: %w", err)
	}

	return store, nil
}

func (s *SQLiteStore) migrate() error {
	migrations := []string{
		`CREATE TABLE IF NOT EXISTS vehicles (
			id TEXT PRIMARY KEY,
			phone TEXT NOT NULL,
			protocol TEXT NOT NULL DEFAULT 'jt808',
			plate_no TEXT DEFAULT '',
			plate_color INTEGER DEFAULT 0,
			terminal_id TEXT DEFAULT '',
			terminal_type TEXT DEFAULT '',
			manufacturer TEXT DEFAULT '',
			province_id INTEGER DEFAULT 0,
			city_id INTEGER DEFAULT 0,
			online INTEGER DEFAULT 0,
			registered_at DATETIME NOT NULL,
			last_active DATETIME NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_vehicles_phone ON vehicles(phone)`,
		`CREATE INDEX IF NOT EXISTS idx_vehicles_online ON vehicles(online)`,
		`CREATE TABLE IF NOT EXISTS locations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			vehicle_id TEXT NOT NULL,
			phone TEXT NOT NULL,
			latitude REAL NOT NULL,
			longitude REAL NOT NULL,
			altitude REAL DEFAULT 0,
			speed REAL DEFAULT 0,
			direction INTEGER DEFAULT 0,
			time DATETIME,
			alarm_flag INTEGER DEFAULT 0,
			status_flag INTEGER DEFAULT 0,
			mileage REAL DEFAULT 0,
			fuel REAL DEFAULT 0,
			received_at DATETIME NOT NULL,
			source TEXT DEFAULT 'jt808'
		)`,
		`CREATE INDEX IF NOT EXISTS idx_locations_vehicle_id ON locations(vehicle_id)`,
		`CREATE INDEX IF NOT EXISTS idx_locations_received_at ON locations(received_at)`,
		// 复合索引：按 vehicle_id + received_at 时间范围查询（轨迹回放核心查询路径）
		`CREATE INDEX IF NOT EXISTS idx_locations_vehicle_time ON locations(vehicle_id, received_at)`,
		`CREATE TABLE IF NOT EXISTS alarms (
			id TEXT PRIMARY KEY,
			vehicle_id TEXT NOT NULL,
			phone TEXT NOT NULL,
			type TEXT DEFAULT '',
			level INTEGER DEFAULT 0,
			alarm_flag INTEGER DEFAULT 0,
			latitude REAL DEFAULT 0,
			longitude REAL DEFAULT 0,
			altitude REAL DEFAULT 0,
			speed REAL DEFAULT 0,
			direction INTEGER DEFAULT 0,
			time DATETIME,
			additional BLOB,
			received_at DATETIME NOT NULL,
			source TEXT DEFAULT 'jt808'
		)`,
		`CREATE INDEX IF NOT EXISTS idx_alarms_vehicle_id ON alarms(vehicle_id)`,
		`CREATE INDEX IF NOT EXISTS idx_alarms_received_at ON alarms(received_at)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			phone TEXT NOT NULL,
			protocol TEXT NOT NULL DEFAULT 'jt808',
			remote_addr TEXT DEFAULT '',
			status TEXT DEFAULT 'connected',
			registered_at DATETIME NOT NULL,
			last_active DATETIME NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_phone ON sessions(phone)`,
		`CREATE TABLE IF NOT EXISTS protocol_logs (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			phone TEXT NOT NULL,
			protocol TEXT NOT NULL DEFAULT 'jt808',
			msg_type INTEGER NOT NULL DEFAULT 0,
			msg_name TEXT DEFAULT '',
			direction TEXT NOT NULL DEFAULT 'up',
			raw_hex TEXT DEFAULT '',
			length INTEGER DEFAULT 0,
			received_at DATETIME NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_proto_logs_phone ON protocol_logs(phone)`,
		`CREATE INDEX IF NOT EXISTS idx_proto_logs_received_at ON protocol_logs(received_at)`,
		`CREATE TABLE IF NOT EXISTS driver_info (
			id TEXT PRIMARY KEY,
			vehicle_id TEXT NOT NULL,
			phone TEXT NOT NULL,
			driver_name TEXT DEFAULT '',
			license_id TEXT DEFAULT '',
			license_org TEXT DEFAULT '',
			license_expiry TEXT DEFAULT '',
			id_card TEXT DEFAULT '',
			received_at DATETIME NOT NULL,
			source TEXT DEFAULT 'jt808'
		)`,
		`CREATE INDEX IF NOT EXISTS idx_driver_info_vehicle_id ON driver_info(vehicle_id)`,
		`CREATE TABLE IF NOT EXISTS multimedia (
			id TEXT PRIMARY KEY,
			vehicle_id TEXT NOT NULL,
			phone TEXT NOT NULL,
			media_type INTEGER DEFAULT 0,
			media_format INTEGER DEFAULT 0,
			event_item INTEGER DEFAULT 0,
			channel_id INTEGER DEFAULT 0,
			latitude REAL DEFAULT 0,
			longitude REAL DEFAULT 0,
			received_at DATETIME NOT NULL,
			source TEXT DEFAULT 'jt808'
		)`,
		`CREATE INDEX IF NOT EXISTS idx_multimedia_vehicle_id ON multimedia(vehicle_id)`,
		`CREATE TABLE IF NOT EXISTS can_data (
			id TEXT PRIMARY KEY,
			vehicle_id TEXT NOT NULL,
			phone TEXT NOT NULL,
			items TEXT NOT NULL DEFAULT '[]',
			received_at DATETIME NOT NULL,
			source TEXT DEFAULT 'jt808'
		)`,
		`CREATE INDEX IF NOT EXISTS idx_can_data_vehicle_id ON can_data(vehicle_id)`,
		`CREATE TABLE IF NOT EXISTS bd_nav_data (
			id TEXT PRIMARY KEY,
			vehicle_id TEXT NOT NULL,
			phone TEXT NOT NULL,
			sat_count INTEGER DEFAULT 0,
			latitude REAL DEFAULT 0,
			longitude REAL DEFAULT 0,
			altitude INTEGER DEFAULT 0,
			speed INTEGER DEFAULT 0,
			direction INTEGER DEFAULT 0,
			bd_time TEXT DEFAULT '',
			received_at DATETIME NOT NULL,
			source TEXT DEFAULT 'jt808'
		)`,
		`CREATE INDEX IF NOT EXISTS idx_bd_nav_data_vehicle_id ON bd_nav_data(vehicle_id)`,
		`CREATE TABLE IF NOT EXISTS meter_data (
			id TEXT PRIMARY KEY,
			vehicle_id TEXT NOT NULL,
			phone TEXT NOT NULL,
			meter_value REAL DEFAULT 0,
			received_at DATETIME NOT NULL,
			source TEXT DEFAULT 'jt808'
		)`,
		`CREATE INDEX IF NOT EXISTS idx_meter_data_vehicle_id ON meter_data(vehicle_id)`,
		`CREATE TABLE IF NOT EXISTS dispatch_data (
			id TEXT PRIMARY KEY,
			vehicle_id TEXT NOT NULL,
			phone TEXT NOT NULL,
			content TEXT DEFAULT '',
			received_at DATETIME NOT NULL,
			source TEXT DEFAULT 'jt808'
		)`,
		`CREATE INDEX IF NOT EXISTS idx_dispatch_data_vehicle_id ON dispatch_data(vehicle_id)`,
		`CREATE TABLE IF NOT EXISTS electronic_waybills (
			id TEXT PRIMARY KEY,
			phone TEXT NOT NULL,
			waybill_no TEXT DEFAULT '',
			content TEXT DEFAULT '',
			received_at DATETIME NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_waybills_phone ON electronic_waybills(phone)`,
		`CREATE TABLE IF NOT EXISTS command_resps (
			id TEXT PRIMARY KEY,
			phone TEXT NOT NULL,
			command_id TEXT DEFAULT '',
			result INTEGER DEFAULT 0,
			received_at DATETIME NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_command_resps_phone ON command_resps(phone)`,
		`CREATE TABLE IF NOT EXISTS terminal_props (
			id TEXT PRIMARY KEY,
			phone TEXT NOT NULL,
			manufacturer_id TEXT DEFAULT '',
			model TEXT DEFAULT '',
			hardware_version TEXT DEFAULT '',
			firmware_version TEXT DEFAULT '',
			gnss_support INTEGER DEFAULT 0,
			comm_module INTEGER DEFAULT 0,
			received_at DATETIME NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_terminal_props_phone ON terminal_props(phone)`,
		`CREATE TABLE IF NOT EXISTS av_params (
			id TEXT PRIMARY KEY,
			phone TEXT NOT NULL,
			channel_id INTEGER DEFAULT 0,
			param_type INTEGER DEFAULT 0,
			param_value TEXT DEFAULT '',
			received_at DATETIME NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_av_params_phone ON av_params(phone)`,
		`CREATE TABLE IF NOT EXISTS info_menu_resps (
			id TEXT PRIMARY KEY,
			phone TEXT NOT NULL,
			info_type INTEGER DEFAULT 0,
			info_id INTEGER DEFAULT 0,
			info_data TEXT DEFAULT '',
			received_at DATETIME NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_info_menu_resps_phone ON info_menu_resps(phone)`,
		`CREATE TABLE IF NOT EXISTS sms_forward_resps (
			id TEXT PRIMARY KEY,
			phone TEXT NOT NULL,
			sms_content TEXT DEFAULT '',
			received_at DATETIME NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sms_forward_resps_phone ON sms_forward_resps(phone)`,
		`CREATE TABLE IF NOT EXISTS event_resps (
			id TEXT PRIMARY KEY,
			phone TEXT NOT NULL,
			event_id INTEGER DEFAULT 0,
			received_at DATETIME NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_event_resps_phone ON event_resps(phone)`,
		// AUTO-FIX-2026-06-29: GB/T 32960 电动汽车数据表
		`CREATE TABLE IF NOT EXISTS ev_data (
			id TEXT PRIMARY KEY,
			vehicle_id TEXT NOT NULL,
			phone TEXT,
			data_type TEXT NOT NULL,
			data BLOB,
			received_at DATETIME NOT NULL,
			source TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_ev_data_vehicle_id ON ev_data(vehicle_id)`,
		`CREATE INDEX IF NOT EXISTS idx_ev_data_received_at ON ev_data(received_at)`,
		`CREATE INDEX IF NOT EXISTS idx_ev_data_vehicle_type ON ev_data(vehicle_id, data_type)`,
	}

	for _, m := range migrations {
		if _, err := s.db.Exec(m); err != nil {
			return fmt.Errorf("exec migration: %w", err)
		}
	}

	s.logger.Info("sqlite migrations completed")
	return nil
}

func (s *SQLiteStore) SaveVehicle(ctx context.Context, vehicle *storage.Vehicle) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO vehicles (id, phone, protocol, plate_no, plate_color, terminal_id, terminal_type, manufacturer, province_id, city_id, online, registered_at, last_active)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		vehicle.ID, vehicle.Phone, vehicle.Protocol, vehicle.PlateNo, vehicle.PlateColor,
		vehicle.TerminalID, vehicle.TerminalType, vehicle.Manufacturer, vehicle.ProvinceID,
		vehicle.CityID, boolToInt(vehicle.Online), vehicle.RegisteredAt, vehicle.LastActive)
	return err
}

func (s *SQLiteStore) GetVehicle(ctx context.Context, id string) (*storage.Vehicle, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, phone, protocol, plate_no, plate_color, terminal_id, terminal_type, manufacturer, province_id, city_id, online, registered_at, last_active
		 FROM vehicles WHERE id = ?`, id)
	return s.scanVehicle(row)
}

func (s *SQLiteStore) GetVehicleByPhone(ctx context.Context, phone string) (*storage.Vehicle, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, phone, protocol, plate_no, plate_color, terminal_id, terminal_type, manufacturer, province_id, city_id, online, registered_at, last_active
		 FROM vehicles WHERE phone = ?`, phone)
	return s.scanVehicle(row)
}

func (s *SQLiteStore) ListVehicles(ctx context.Context, opts storage.ListOptions) (*storage.ListResult, error) {
	where := []string{"1=1"}
	args := []interface{}{}

	if opts.Phone != "" {
		where = append(where, "phone LIKE ?")
		args = append(args, opts.Phone+"%")
	}
	if opts.Online != nil {
		where = append(where, "online = ?")
		args = append(args, boolToInt(*opts.Online))
	}

	whereClause := strings.Join(where, " AND ")

	var total int64
	countSQL := fmt.Sprintf("SELECT COUNT(*) FROM vehicles WHERE %s", whereClause)
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

	orderBy := "registered_at DESC"
	if opts.OrderBy != "" {
		// AUTO-FIX-2026-07-02 [安全]: OrderBy 白名单校验防 SQL 注入
		orderBy = safesql.ValidateOrderBy(opts.OrderBy, "registered_at DESC")
	}

	querySQL := fmt.Sprintf("SELECT id, phone, protocol, plate_no, plate_color, terminal_id, terminal_type, manufacturer, province_id, city_id, online, registered_at, last_active FROM vehicles WHERE %s ORDER BY %s LIMIT ? OFFSET ?", whereClause, orderBy)
	queryArgs := append(args, pageSize, offset)

	rows, err := s.db.QueryContext(ctx, querySQL, queryArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]*storage.Vehicle, 0)
	for rows.Next() {
		v, err := s.scanVehicleRows(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, v)
	}

	return &storage.ListResult{
		Items: items,
		Total: total,
		Page:  opts.Page,
		Size:  pageSize,
	}, nil
}

func (s *SQLiteStore) DeleteVehicle(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM vehicles WHERE id = ?", id)
	return err
}

func (s *SQLiteStore) UpdateVehicleOnline(ctx context.Context, id string, online bool) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE vehicles SET online = ?, last_active = ? WHERE id = ?",
		boolToInt(online), time.Now(), id)
	return err
}

func (s *SQLiteStore) SaveLocation(ctx context.Context, loc *storage.LocationData) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO locations (vehicle_id, phone, latitude, longitude, altitude, speed, direction, time, alarm_flag, status_flag, mileage, fuel, received_at, source)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		loc.VehicleID, loc.Phone, loc.Latitude, loc.Longitude, loc.Altitude,
		loc.Speed, loc.Direction, loc.Time, loc.AlarmFlag, loc.StatusFlag,
		loc.Mileage, loc.Fuel, loc.ReceivedAt, loc.Source)
	return err
}

func (s *SQLiteStore) GetLatestLocation(ctx context.Context, vehicleID string) (*storage.LocationData, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT vehicle_id, phone, latitude, longitude, altitude, speed, direction, time, alarm_flag, status_flag, mileage, fuel, received_at, source
		 FROM locations WHERE vehicle_id = ? ORDER BY received_at DESC LIMIT 1`, vehicleID)
	return s.scanLocation(row)
}

func (s *SQLiteStore) GetLocationTrack(ctx context.Context, vehicleID string, start, end time.Time) ([]*storage.LocationData, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT vehicle_id, phone, latitude, longitude, altitude, speed, direction, time, alarm_flag, status_flag, mileage, fuel, received_at, source
		 FROM locations WHERE vehicle_id = ? AND received_at BETWEEN ? AND ? ORDER BY received_at`,
		vehicleID, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]*storage.LocationData, 0)
	for rows.Next() {
		loc, err := s.scanLocationRows(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, loc)
	}
	return result, nil
}

func (s *SQLiteStore) SaveAlarm(ctx context.Context, alarm *storage.AlarmData) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO alarms (id, vehicle_id, phone, type, level, alarm_flag, latitude, longitude, altitude, speed, direction, time, additional, received_at, source)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		alarm.ID, alarm.VehicleID, alarm.Phone, alarm.Type, alarm.Level,
		alarm.AlarmFlag, alarm.Latitude, alarm.Longitude, alarm.Altitude,
		alarm.Speed, alarm.Direction, alarm.Time, alarm.Additional,
		alarm.ReceivedAt, alarm.Source)
	return err
}

// UpdateAlarm 更新报警数据（按主键 id 更新 additional/level 等字段，用于 AI 过滤结果回写）
func (s *SQLiteStore) UpdateAlarm(ctx context.Context, alarm *storage.AlarmData) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE alarms SET additional = ?, level = ? WHERE id = ?`,
		alarm.Additional, alarm.Level, alarm.ID)
	return err
}

func (s *SQLiteStore) ListAlarms(ctx context.Context, opts storage.ListOptions) (*storage.ListResult, error) {
	where := []string{"1=1"}
	args := []interface{}{}

	if opts.Phone != "" {
		where = append(where, "phone LIKE ?")
		args = append(args, opts.Phone+"%")
	}
	if opts.Start != "" && opts.End != "" {
		where = append(where, "received_at BETWEEN ? AND ?")
		args = append(args, opts.Start, opts.End)
	}

	whereClause := strings.Join(where, " AND ")

	var total int64
	countSQL := fmt.Sprintf("SELECT COUNT(*) FROM alarms WHERE %s", whereClause)
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

	querySQL := fmt.Sprintf("SELECT id, vehicle_id, phone, type, level, alarm_flag, latitude, longitude, altitude, speed, direction, time, additional, received_at, source FROM alarms WHERE %s ORDER BY received_at DESC LIMIT ? OFFSET ?", whereClause)
	queryArgs := append(args, pageSize, offset)

	rows, err := s.db.QueryContext(ctx, querySQL, queryArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]*storage.AlarmData, 0)
	for rows.Next() {
		var a storage.AlarmData
		var additional []byte
		err := rows.Scan(&a.ID, &a.VehicleID, &a.Phone, &a.Type, &a.Level,
			&a.AlarmFlag, &a.Latitude, &a.Longitude, &a.Altitude,
			&a.Speed, &a.Direction, &a.Time, &additional,
			&a.ReceivedAt, &a.Source)
		if err != nil {
			return nil, err
		}
		if additional != nil {
			a.Additional = additional
		}
		items = append(items, &a)
	}

	return &storage.ListResult{
		Items: items,
		Total: total,
		Page:  opts.Page,
		Size:  pageSize,
	}, nil
}

func (s *SQLiteStore) SaveSession(ctx context.Context, session *storage.SessionData) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO sessions (id, phone, protocol, remote_addr, status, registered_at, last_active)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		session.ID, session.Phone, session.Protocol, session.RemoteAddr,
		session.Status, session.RegisteredAt, session.LastActive)
	return err
}

func (s *SQLiteStore) GetSession(ctx context.Context, id string) (*storage.SessionData, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, phone, protocol, remote_addr, status, registered_at, last_active FROM sessions WHERE id = ?`, id)
	var sess storage.SessionData
	err := row.Scan(&sess.ID, &sess.Phone, &sess.Protocol, &sess.RemoteAddr,
		&sess.Status, &sess.RegisteredAt, &sess.LastActive)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &sess, nil
}

func (s *SQLiteStore) ListSessions(ctx context.Context, opts storage.ListOptions) (*storage.ListResult, error) {
	var total int64
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions").Scan(&total); err != nil {
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

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, phone, protocol, remote_addr, status, registered_at, last_active FROM sessions ORDER BY last_active DESC LIMIT ? OFFSET ?`,
		pageSize, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]*storage.SessionData, 0)
	for rows.Next() {
		var sess storage.SessionData
		if err := rows.Scan(&sess.ID, &sess.Phone, &sess.Protocol, &sess.RemoteAddr,
			&sess.Status, &sess.RegisteredAt, &sess.LastActive); err != nil {
			return nil, err
		}
		items = append(items, &sess)
	}

	return &storage.ListResult{
		Items: items,
		Total: total,
		Page:  opts.Page,
		Size:  pageSize,
	}, nil
}

func (s *SQLiteStore) DeleteSession(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE id = ?", id)
	return err
}

func (s *SQLiteStore) GetOnlineCount(ctx context.Context) (int64, error) {
	var count int64
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM vehicles WHERE online = 1").Scan(&count)
	return count, err
}

func (s *SQLiteStore) GetAlarmCount(ctx context.Context, start, end time.Time) (int64, error) {
	var count int64
	err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM alarms WHERE received_at BETWEEN ? AND ?",
		start, end).Scan(&count)
	return count, err
}

func (s *SQLiteStore) GetAlarmCountBySource(ctx context.Context, source string, start, end time.Time) (int64, error) {
	var count int64
	err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM alarms WHERE source = ? AND received_at BETWEEN ? AND ?",
		source, start, end).Scan(&count)
	return count, err
}

func (s *SQLiteStore) ListOnlineLocations(ctx context.Context) ([]*storage.LocationData, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT l.vehicle_id, l.phone, l.latitude, l.longitude, l.altitude, l.speed, l.direction, l.time, l.alarm_flag, l.status_flag, l.mileage, l.fuel, l.received_at, l.source
		 FROM locations l INNER JOIN vehicles v ON l.vehicle_id = v.id
		 WHERE v.online = 1 AND l.id = (SELECT MAX(id) FROM locations WHERE vehicle_id = l.vehicle_id)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]*storage.LocationData, 0)
	for rows.Next() {
		loc, err := s.scanLocationRows(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, loc)
	}
	return result, nil
}

func (s *SQLiteStore) SaveProtocolLog(ctx context.Context, log *storage.ProtocolLog) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO protocol_logs (id, session_id, phone, protocol, msg_type, msg_name, direction, raw_hex, length, received_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		log.ID, log.SessionID, log.Phone, log.Protocol, log.MsgType,
		log.MsgName, log.Direction, log.RawHex, log.Length, log.ReceivedAt)
	return err
}

func (s *SQLiteStore) ListProtocolLogs(ctx context.Context, opts storage.ListOptions) (*storage.ListResult, error) {
	where := []string{"1=1"}
	args := []interface{}{}

	if opts.Phone != "" {
		where = append(where, "phone LIKE ?")
		args = append(args, opts.Phone+"%")
	}

	whereClause := strings.Join(where, " AND ")

	var total int64
	countSQL := fmt.Sprintf("SELECT COUNT(*) FROM protocol_logs WHERE %s", whereClause)
	if err := s.db.QueryRowContext(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, err
	}

	offset := opts.Offset
	if offset == 0 && opts.Page > 0 {
		offset = (opts.Page - 1) * opts.PageSize
	}
	pageSize := opts.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}

	querySQL := fmt.Sprintf("SELECT id, session_id, phone, protocol, msg_type, msg_name, direction, raw_hex, length, received_at FROM protocol_logs WHERE %s ORDER BY received_at DESC LIMIT ? OFFSET ?", whereClause)
	queryArgs := append(args, pageSize, offset)

	rows, err := s.db.QueryContext(ctx, querySQL, queryArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]*storage.ProtocolLog, 0)
	for rows.Next() {
		var l storage.ProtocolLog
		if err := rows.Scan(&l.ID, &l.SessionID, &l.Phone, &l.Protocol,
			&l.MsgType, &l.MsgName, &l.Direction, &l.RawHex, &l.Length,
			&l.ReceivedAt); err != nil {
			return nil, err
		}
		items = append(items, &l)
	}

	return &storage.ListResult{
		Items: items,
		Total: total,
		Page:  opts.Page,
		Size:  pageSize,
	}, nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// AUTO-FIX-2026-06-26: 第五轮存储修复 - 数据归档/清理方法
// CleanupOldLocations 删除指定时间之前的位置数据，返回删除行数。
func (s *SQLiteStore) CleanupOldLocations(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, "DELETE FROM locations WHERE received_at < ?", before.Format("2006-01-02 15:04:05"))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CleanupOldAlarms 删除指定时间之前的报警数据，返回删除行数。
func (s *SQLiteStore) CleanupOldAlarms(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, "DELETE FROM alarms WHERE received_at < ?", before.Format("2006-01-02 15:04:05"))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CleanupOldProtocolLogs 删除指定时间之前的协议日志，返回删除行数。
func (s *SQLiteStore) CleanupOldProtocolLogs(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, "DELETE FROM protocol_logs WHERE received_at < ?", before.Format("2006-01-02 15:04:05"))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CleanupOldEVData 删除指定时间之前的电动汽车数据，返回删除行数。
func (s *SQLiteStore) CleanupOldEVData(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, "DELETE FROM ev_data WHERE received_at < ?", before.Format("2006-01-02 15:04:05"))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *SQLiteStore) scanVehicle(row *sql.Row) (*storage.Vehicle, error) {
	var v storage.Vehicle
	var online int
	err := row.Scan(&v.ID, &v.Phone, &v.Protocol, &v.PlateNo, &v.PlateColor,
		&v.TerminalID, &v.TerminalType, &v.Manufacturer, &v.ProvinceID,
		&v.CityID, &online, &v.RegisteredAt, &v.LastActive)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	v.Online = online == 1
	return &v, nil
}

func (s *SQLiteStore) scanVehicleRows(rows *sql.Rows) (*storage.Vehicle, error) {
	var v storage.Vehicle
	var online int
	err := rows.Scan(&v.ID, &v.Phone, &v.Protocol, &v.PlateNo, &v.PlateColor,
		&v.TerminalID, &v.TerminalType, &v.Manufacturer, &v.ProvinceID,
		&v.CityID, &online, &v.RegisteredAt, &v.LastActive)
	if err != nil {
		return nil, err
	}
	v.Online = online == 1
	return &v, nil
}

func (s *SQLiteStore) scanLocation(row *sql.Row) (*storage.LocationData, error) {
	var loc storage.LocationData
	err := row.Scan(&loc.VehicleID, &loc.Phone, &loc.Latitude, &loc.Longitude,
		&loc.Altitude, &loc.Speed, &loc.Direction, &loc.Time,
		&loc.AlarmFlag, &loc.StatusFlag, &loc.Mileage, &loc.Fuel,
		&loc.ReceivedAt, &loc.Source)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &loc, nil
}

func (s *SQLiteStore) scanLocationRows(rows *sql.Rows) (*storage.LocationData, error) {
	var loc storage.LocationData
	err := rows.Scan(&loc.VehicleID, &loc.Phone, &loc.Latitude, &loc.Longitude,
		&loc.Altitude, &loc.Speed, &loc.Direction, &loc.Time,
		&loc.AlarmFlag, &loc.StatusFlag, &loc.Mileage, &loc.Fuel,
		&loc.ReceivedAt, &loc.Source)
	if err != nil {
		return nil, err
	}
	return &loc, nil
}

func (s *SQLiteStore) GetOfflineCount(ctx context.Context) (int64, error) {
	var count int64
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM vehicles WHERE online = 0").Scan(&count)
	return count, err
}

func (s *SQLiteStore) SaveDriverInfo(ctx context.Context, info *storage.DriverInfoData) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO driver_info (id, vehicle_id, phone, driver_name, license_id, license_org, license_expiry, id_card, received_at, source)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		info.ID, info.VehicleID, info.Phone, info.DriverName, info.LicenseID,
		info.LicenseOrg, info.LicenseExpiry, info.IDCard, info.ReceivedAt, info.Source)
	return err
}

func (s *SQLiteStore) SaveMultimedia(ctx context.Context, media *storage.MultimediaData) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO multimedia (id, vehicle_id, phone, media_type, media_format, event_item, channel_id, latitude, longitude, received_at, source)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		media.ID, media.VehicleID, media.Phone, media.MediaType, media.MediaFormat,
		media.EventItem, media.ChannelID, media.Latitude, media.Longitude, media.ReceivedAt, media.Source)
	return err
}

func (s *SQLiteStore) QueryMultimedia(ctx context.Context, vehicleID string, channelID int, start, end time.Time, limit int) ([]*storage.MultimediaData, error) {
	if limit <= 0 {
		limit = 100
	}
	query := `SELECT id, vehicle_id, phone, media_type, media_format, event_item, channel_id, latitude, longitude, received_at, source
		FROM multimedia WHERE 1=1`
	args := []interface{}{}
	if vehicleID != "" {
		query += ` AND vehicle_id = ?`
		args = append(args, vehicleID)
	}
	if channelID >= 0 {
		query += ` AND channel_id = ?`
		args = append(args, channelID)
	}
	if !start.IsZero() {
		query += ` AND received_at >= ?`
		args = append(args, start)
	}
	if !end.IsZero() {
		query += ` AND received_at <= ?`
		args = append(args, end)
	}
	query += ` ORDER BY received_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query multimedia: %w", err)
	}
	defer rows.Close()

	result := make([]*storage.MultimediaData, 0, limit)
	for rows.Next() {
		media := &storage.MultimediaData{}
		if err := rows.Scan(&media.ID, &media.VehicleID, &media.Phone, &media.MediaType, &media.MediaFormat,
			&media.EventItem, &media.ChannelID, &media.Latitude, &media.Longitude, &media.ReceivedAt, &media.Source); err != nil {
			return nil, fmt.Errorf("scan multimedia: %w", err)
		}
		result = append(result, media)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) SaveCanData(ctx context.Context, can *storage.CanBusData) error {
	itemsJSON, err := json.Marshal(can.Items)
	if err != nil {
		return fmt.Errorf("marshal can items: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO can_data (id, vehicle_id, phone, items, received_at, source)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		can.ID, can.VehicleID, can.Phone, string(itemsJSON), can.ReceivedAt, can.Source)
	return err
}

func (s *SQLiteStore) SaveBDNavData(ctx context.Context, bd *storage.BDNavData) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO bd_nav_data (id, vehicle_id, phone, sat_count, latitude, longitude, altitude, speed, direction, bd_time, received_at, source)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		bd.ID, bd.VehicleID, bd.Phone, bd.SatCount, bd.Latitude, bd.Longitude,
		bd.Altitude, bd.Speed, bd.Direction, bd.BDTime, bd.ReceivedAt, bd.Source)
	return err
}

func (s *SQLiteStore) SaveMeterData(ctx context.Context, meter *storage.MeterData) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO meter_data (id, vehicle_id, phone, meter_value, received_at, source)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		meter.ID, meter.VehicleID, meter.Phone, meter.MeterValue, meter.ReceivedAt, meter.Source)
	return err
}

func (s *SQLiteStore) SaveDispatch(ctx context.Context, dispatch *storage.DispatchData) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO dispatch_data (id, vehicle_id, phone, content, received_at, source)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		dispatch.ID, dispatch.VehicleID, dispatch.Phone, dispatch.Content, dispatch.ReceivedAt, dispatch.Source)
	return err
}

func (s *SQLiteStore) SaveElectronicWaybill(ctx context.Context, wb *storage.ElectronicWaybillData) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO electronic_waybills (id, phone, waybill_no, content, received_at)
		 VALUES (?, ?, ?, ?, ?)`,
		wb.ID, wb.Phone, wb.WaybillNo, wb.Content, wb.ReceivedAt)
	return err
}

func (s *SQLiteStore) SaveCommandResp(ctx context.Context, resp *storage.CommandRespData) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO command_resps (id, phone, command_id, result, received_at)
		 VALUES (?, ?, ?, ?, ?)`,
		resp.ID, resp.Phone, resp.CommandID, resp.Result, resp.ReceivedAt)
	return err
}

func (s *SQLiteStore) SaveTerminalProp(ctx context.Context, prop *storage.TerminalPropData) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO terminal_props (id, phone, manufacturer_id, model, hardware_version, firmware_version, gnss_support, comm_module, received_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		prop.ID, prop.Phone, prop.ManufacturerID, prop.Model, prop.HardwareVersion,
		prop.FirmwareVersion, prop.GNSSSupport, prop.CommModule, prop.ReceivedAt)
	return err
}

func (s *SQLiteStore) SaveAVParam(ctx context.Context, param *storage.AVParamData) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO av_params (id, phone, channel_id, param_type, param_value, received_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		param.ID, param.Phone, param.ChannelID, param.ParamType, param.ParamValue, param.ReceivedAt)
	return err
}

func (s *SQLiteStore) ListTerminalProps(ctx context.Context, opts storage.ListOptions) (*storage.ListResult, error) {
	query := `SELECT id, phone, manufacturer_id, model, hardware_version, firmware_version, gnss_support, comm_module, received_at FROM terminal_props ORDER BY received_at DESC`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []*storage.TerminalPropData
	for rows.Next() {
		p := &storage.TerminalPropData{}
		if err := rows.Scan(&p.ID, &p.Phone, &p.ManufacturerID, &p.Model, &p.HardwareVersion,
			&p.FirmwareVersion, &p.GNSSSupport, &p.CommModule, &p.ReceivedAt); err != nil {
			return nil, err
		}
		items = append(items, p)
	}
	return &storage.ListResult{Items: items, Total: int64(len(items))}, nil
}

func (s *SQLiteStore) SaveInfoMenuResp(ctx context.Context, resp *storage.InfoMenuRespData) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO info_menu_resps (id, phone, info_type, info_id, info_data, received_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		resp.ID, resp.Phone, resp.InfoType, resp.InfoID, resp.InfoData, resp.ReceivedAt)
	return err
}

func (s *SQLiteStore) SaveSMSForwardResp(ctx context.Context, resp *storage.SMSForwardRespData) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sms_forward_resps (id, phone, sms_content, received_at)
		 VALUES (?, ?, ?, ?)`,
		resp.ID, resp.Phone, resp.SMSContent, resp.ReceivedAt)
	return err
}

func (s *SQLiteStore) SaveEventResp(ctx context.Context, resp *storage.EventRespData) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO event_resps (id, phone, event_id, received_at)
		 VALUES (?, ?, ?, ?)`,
		resp.ID, resp.Phone, resp.EventID, resp.ReceivedAt)
	return err
}

// AUTO-FIX-2026-06-29: GB/T 32960 电动汽车数据持久化
func (s *SQLiteStore) SaveEVData(ctx context.Context, data *storage.EVData) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO ev_data (id, vehicle_id, phone, data_type, data, received_at, source)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		data.ID, data.VehicleID, data.Phone, data.DataType, data.Data, data.ReceivedAt, data.Source)
	return err
}

func (s *SQLiteStore) QueryEVData(ctx context.Context, vehicleID string, dataType string, start, end time.Time, limit int) ([]*storage.EVData, error) {
	if limit <= 0 {
		limit = 100
	}
	var query string
	var args []interface{}
	if dataType != "" {
		query = `SELECT id, vehicle_id, phone, data_type, data, received_at, source FROM ev_data WHERE vehicle_id = ? AND data_type = ? AND received_at >= ? AND received_at < ? ORDER BY received_at DESC LIMIT ?`
		args = []interface{}{vehicleID, dataType, start, end, limit}
	} else {
		query = `SELECT id, vehicle_id, phone, data_type, data, received_at, source FROM ev_data WHERE vehicle_id = ? AND received_at >= ? AND received_at < ? ORDER BY received_at DESC LIMIT ?`
		args = []interface{}{vehicleID, start, end, limit}
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []*storage.EVData
	for rows.Next() {
		d := &storage.EVData{}
		if err := rows.Scan(&d.ID, &d.VehicleID, &d.Phone, &d.DataType, &d.Data, &d.ReceivedAt, &d.Source); err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) BatchSaveLocations(ctx context.Context, locations []*storage.LocationData) error {
	if len(locations) == 0 {
		return nil
	}
	const batchSize = 1000
	const insertSQL = `INSERT INTO locations (vehicle_id, phone, latitude, longitude, altitude, speed, direction, time, alarm_flag, status_flag, mileage, fuel, received_at, source)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	for start := 0; start < len(locations); start += batchSize {
		end := start + batchSize
		if end > len(locations) {
			end = len(locations)
		}
		batch := locations[start:end]
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin tx: %w", err)
		}
		stmt, err := tx.PrepareContext(ctx, insertSQL)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("prepare: %w", err)
		}
		for _, loc := range batch {
			if _, err := stmt.ExecContext(ctx,
				loc.VehicleID, loc.Phone, loc.Latitude, loc.Longitude, loc.Altitude,
				loc.Speed, loc.Direction, loc.Time, loc.AlarmFlag, loc.StatusFlag,
				loc.Mileage, loc.Fuel, loc.ReceivedAt, loc.Source); err != nil {
				tx.Rollback()
				stmt.Close()
				return fmt.Errorf("exec: %w", err)
			}
		}
		stmt.Close()
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit: %w", err)
		}
	}
	return nil
}

func (s *SQLiteStore) BatchSaveAlarms(ctx context.Context, alarms []*storage.AlarmData) error {
	if len(alarms) == 0 {
		return nil
	}
	const batchSize = 1000
	const insertSQL = `INSERT INTO alarms (id, vehicle_id, phone, type, level, alarm_flag, latitude, longitude, altitude, speed, direction, time, additional, received_at, source)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	for start := 0; start < len(alarms); start += batchSize {
		end := start + batchSize
		if end > len(alarms) {
			end = len(alarms)
		}
		batch := alarms[start:end]
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin tx: %w", err)
		}
		stmt, err := tx.PrepareContext(ctx, insertSQL)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("prepare: %w", err)
		}
		for _, alarm := range batch {
			if _, err := stmt.ExecContext(ctx,
				alarm.ID, alarm.VehicleID, alarm.Phone, alarm.Type, alarm.Level,
				alarm.AlarmFlag, alarm.Latitude, alarm.Longitude, alarm.Altitude,
				alarm.Speed, alarm.Direction, alarm.Time, alarm.Additional,
				alarm.ReceivedAt, alarm.Source); err != nil {
				tx.Rollback()
				stmt.Close()
				return fmt.Errorf("exec: %w", err)
			}
		}
		stmt.Close()
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit: %w", err)
		}
	}
	return nil
}

func (s *SQLiteStore) BatchSaveProtocolLogs(ctx context.Context, logs []*storage.ProtocolLog) error {
	if len(logs) == 0 {
		return nil
	}
	const batchSize = 1000
	const insertSQL = `INSERT INTO protocol_logs (id, session_id, phone, protocol, msg_type, msg_name, direction, raw_hex, length, received_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	for start := 0; start < len(logs); start += batchSize {
		end := start + batchSize
		if end > len(logs) {
			end = len(logs)
		}
		batch := logs[start:end]
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin tx: %w", err)
		}
		stmt, err := tx.PrepareContext(ctx, insertSQL)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("prepare: %w", err)
		}
		for _, log := range batch {
			if _, err := stmt.ExecContext(ctx,
				log.ID, log.SessionID, log.Phone, log.Protocol, log.MsgType,
				log.MsgName, log.Direction, log.RawHex, log.Length, log.ReceivedAt); err != nil {
				tx.Rollback()
				stmt.Close()
				return fmt.Errorf("exec: %w", err)
			}
		}
		stmt.Close()
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit: %w", err)
		}
	}
	return nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
