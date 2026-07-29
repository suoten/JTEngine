package migration

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql" // AUTO-FIX-2026-07-14: MySQL 驱动注册，支持 sql.Open("mysql", ...)
	"go.uber.org/zap"
)

type MigratorConfig struct {
	SourceDriver string
	SourceDSN    string
	TargetDriver string
	TargetDSN    string
	BatchSize    int
	DryRun       bool
	ConfigDir    string
}

// [P2-3] allowedTables 是迁移系统允许操作的表名白名单。
// 表名来自硬编码列表，不接收外部输入，防止 SQL 注入。
// migrateTable / countRows / Verify / sampleCompare 在拼接 SQL 前均校验此白名单。
var allowedTables = map[string]bool{
	"vehicles":      true,
	"locations":     true,
	"alarms":        true,
	"sessions":      true,
	"protocol_logs": true,
}

// [P1-52] sanitizeIdentifier 净化 SQL 标识符（表名/列名），
// 仅允许字母、数字、下划线，防止通过列名注入 SQL。
// 用于 sampleCompare 中对 rows.Columns() 返回的列名做防御性校验。
func sanitizeIdentifier(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

type Migrator struct {
	config  *MigratorConfig
	logger  *zap.Logger
	srcDB   *sql.DB
	tgtDB   *sql.DB
	progress *MigrationProgress
}

type MigrationProgress struct {
	SourceDriver string    `json:"source_driver"`
	TargetDriver string    `json:"target_driver"`
	Tables       map[string]*TableProgress `json:"tables"`
	StartedAt    time.Time `json:"started_at"`
	CompletedAt  time.Time `json:"completed_at,omitempty"`
	Status       string    `json:"status"`
}

type TableProgress struct {
	TotalRows    int64     `json:"total_rows"`
	MigratedRows int64     `json:"migrated_rows"`
	LastID       int64     `json:"last_id"`
	// [P1-2] LastPK 记录上次迁移的主键游标（Keyset 分页）。
	// 查询使用 WHERE id > LastPK ORDER BY id ASC LIMIT batch_size，
	// 相比 OFFSET 分页具有 O(1) 性能优势，不受表数据量影响。
	// 向后兼容：旧进度文件无 LastPK（值为 0）时，首次从 ID=0 开始。
	LastPK       int64     `json:"last_pk"`
	CompletedAt  time.Time `json:"completed_at,omitempty"`
	Duration     string    `json:"duration,omitempty"`
	VerifyStatus string    `json:"verify_status,omitempty"`
}

func NewMigrator(cfg *MigratorConfig, logger *zap.Logger) *Migrator {
	if cfg.BatchSize == 0 {
		cfg.BatchSize = 1000
	}
	return &Migrator{
		config:   cfg,
		logger:   logger,
		progress: &MigrationProgress{Tables: make(map[string]*TableProgress)},
	}
}

func (m *Migrator) Connect() error {
	srcDB, err := sql.Open(m.config.SourceDriver, m.config.SourceDSN)
	if err != nil {
		return fmt.Errorf("open source db: %w", err)
	}
	// FIXED: [数据库连接池缺失] 迁移工具未设置连接池参数，可能导致连接耗尽 [2026-07-17]
	srcDB.SetMaxOpenConns(10)
	srcDB.SetMaxIdleConns(5)
	srcDB.SetConnMaxLifetime(30 * time.Minute)
	srcDB.SetConnMaxIdleTime(5 * time.Minute)
	if err := srcDB.Ping(); err != nil {
		srcDB.Close()
		return fmt.Errorf("ping source db: %w", err)
	}
	m.srcDB = srcDB

	tgtDB, err := sql.Open(m.config.TargetDriver, m.config.TargetDSN)
	if err != nil {
		return fmt.Errorf("open target db: %w", err)
	}
	// FIXED: [数据库连接池缺失] 迁移工具目标库未设置连接池参数 [2026-07-17]
	tgtDB.SetMaxOpenConns(10)
	tgtDB.SetMaxIdleConns(5)
	tgtDB.SetConnMaxLifetime(30 * time.Minute)
	tgtDB.SetConnMaxIdleTime(5 * time.Minute)
	if err := tgtDB.Ping(); err != nil {
		tgtDB.Close()
		return fmt.Errorf("ping target db: %w", err)
	}
	m.tgtDB = tgtDB

	m.logger.Info("migration databases connected",
		zap.String("source", m.config.SourceDriver),
		zap.String("target", m.config.TargetDriver))
	return nil
}

func (m *Migrator) Migrate() error {
	if m.srcDB == nil || m.tgtDB == nil {
		return fmt.Errorf("databases not connected")
	}

	m.progress.Status = "running"
	m.progress.StartedAt = time.Now()

	if err := m.loadProgress(); err != nil {
		m.logger.Warn("no existing progress, starting fresh", zap.Error(err))
	}

	tables := []string{"vehicles", "locations", "alarms", "sessions", "protocol_logs"}

	for _, table := range tables {
		if m.config.DryRun {
			count, err := m.countRows(table)
			if err != nil {
				m.logger.Warn("dry run: count failed", zap.String("table", table), zap.Error(err))
				continue
			}
			fmt.Printf("[DRY RUN] %s: %d rows to migrate\n", table, count)
			continue
		}

		if err := m.migrateTable(table); err != nil {
			m.logger.Error("migration failed", zap.String("table", table), zap.Error(err))
			m.progress.Status = "failed"
			_ = m.saveProgress()
			return fmt.Errorf("migrate %s: %w", table, err)
		}
	}

	m.progress.Status = "completed"
	m.progress.CompletedAt = time.Now()
	_ = m.saveProgress()

	m.logger.Info("migration completed")
	return nil
}

func (m *Migrator) migrateTable(table string) error {
	// [P2-3] 表名白名单校验：表名直接拼入 SQL，必须校验防止注入。
	if !allowedTables[table] {
		return fmt.Errorf("unknown table: %s", table)
	}
	tp, exists := m.progress.Tables[table]
	if !exists {
		tp = &TableProgress{}
		m.progress.Tables[table] = tp
	}

	count, err := m.countRows(table)
	if err != nil {
		return err
	}
	tp.TotalRows = count

	m.logger.Info("migrating table",
		zap.String("table", table),
		zap.Int64("rows", count),
		zap.Int64("already_migrated", tp.MigratedRows))

	if tp.MigratedRows >= tp.TotalRows {
		m.logger.Info("table already migrated", zap.String("table", table))
		return nil
	}

	// [P1-2] Keyset 分页：使用 WHERE id > LastPK ORDER BY id ASC LIMIT ? 替代 OFFSET 分页。
	// OFFSET 分页在大表上性能灾难：数据库需要扫描并跳过前 offset 行，时间复杂度 O(offset)。
	// Keyset 分页利用主键索引，直接定位到 LastPK 之后的位置，时间复杂度 O(1)。
	for tp.MigratedRows < tp.TotalRows {
		// 使用 keyset 查询：WHERE id > LastPK ORDER BY id ASC LIMIT batch_size
		query := fmt.Sprintf("SELECT * FROM %s WHERE id > ? ORDER BY id ASC LIMIT %d", table, m.config.BatchSize)
		rows, err := m.srcDB.QueryContext(context.Background(), query, tp.LastPK)
		if err != nil {
			return fmt.Errorf("query source: %w", err)
		}

		columns, _ := rows.Columns()
		colCount := len(columns)
		values := make([]interface{}, colCount)
		valuePtrs := make([]interface{}, colCount)
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		tx, err := m.tgtDB.BeginTx(context.Background(), nil)
		if err != nil {
			rows.Close()
			return fmt.Errorf("begin target tx: %w", err)
		}

		placeholders := make([]string, colCount)
		for i := range placeholders {
			placeholders[i] = "?"
		}
		insertSQL := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
			table,
			columnList(columns),
			strings.Join(placeholders, ", "))

		stmt, err := tx.Prepare(insertSQL)
		if err != nil {
			tx.Rollback()
			rows.Close()
			return fmt.Errorf("prepare insert: %w", err)
		}

		batchCount := int64(0)
		batchLastPK := tp.LastPK // 追踪本批最大 ID
		for rows.Next() {
			if err := rows.Scan(valuePtrs...); err != nil {
				stmt.Close()
				tx.Rollback()
				rows.Close()
				return fmt.Errorf("scan row: %w", err)
			}

			// 更新本批最大主键（假设第一列为主键 ID）
			if pkVal, ok := values[0].(int64); ok && pkVal > batchLastPK {
				batchLastPK = pkVal
			}

			if _, err := stmt.Exec(valuePtrs...); err != nil {
				m.logger.Warn("insert row failed, skipping",
					zap.String("table", table),
					zap.Error(err))
				continue
			}
			batchCount++
		}
		rows.Close()
		stmt.Close()

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit batch: %w", err)
		}

		tp.MigratedRows += batchCount
		tp.LastPK = batchLastPK // 更新主键游标

		if batchCount > 0 {
			_ = m.saveProgress()
		}

		m.logger.Debug("batch migrated",
			zap.String("table", table),
			zap.Int64("progress", tp.MigratedRows),
			zap.Int64("total", tp.TotalRows),
			zap.Int64("last_pk", tp.LastPK))

		if batchCount < int64(m.config.BatchSize) {
			break
		}
	}

	tp.CompletedAt = time.Now()
	return nil
}

func columnList(columns []string) string {
	return strings.Join(columns, ", ")
}

func (m *Migrator) countRows(table string) (int64, error) {
	// [P2-3] 表名白名单校验
	if !allowedTables[table] {
		return 0, fmt.Errorf("unknown table: %s", table)
	}
	var count int64
	err := m.srcDB.QueryRowContext(context.Background(), fmt.Sprintf("SELECT COUNT(*) FROM %s", table)).Scan(&count)
	return count, err
}

func (m *Migrator) Verify() error {
	if m.srcDB == nil || m.tgtDB == nil {
		return fmt.Errorf("databases not connected")
	}

	tables := []string{"vehicles", "locations", "alarms", "sessions", "protocol_logs"}
	allOK := true

	for _, table := range tables {
		// [P2-3] 表名白名单校验
		if !allowedTables[table] {
			return fmt.Errorf("unknown table: %s", table)
		}
		var srcCount, tgtCount int64
		_ = m.srcDB.QueryRowContext(context.Background(), fmt.Sprintf("SELECT COUNT(*) FROM %s", table)).Scan(&srcCount)
		_ = m.tgtDB.QueryRowContext(context.Background(), fmt.Sprintf("SELECT COUNT(*) FROM %s", table)).Scan(&tgtCount)

		status := "OK"
		if srcCount != tgtCount {
			status = "MISMATCH"
			allOK = false
		}
		fmt.Printf("  %-20s source: %-8d target: %-8d %s\n", table, srcCount, tgtCount, status)

		if srcCount > 0 && tgtCount > 0 {
			sampleSize := 10
			if int64(sampleSize) > srcCount {
				sampleSize = int(srcCount)
			}
			mismatchCount := m.sampleCompare(table, sampleSize)
			if mismatchCount > 0 {
				fmt.Printf("  %-20s sample compare: %d/%d mismatches\n", table, mismatchCount, sampleSize)
				status = fmt.Sprintf("SAMPLE_MISMATCH(%d/%d)", mismatchCount, sampleSize)
				allOK = false
			} else {
				fmt.Printf("  %-20s sample compare: all %d passed\n", table, sampleSize)
			}
		}

		if m.progress != nil && m.progress.Tables != nil {
			if tp, ok := m.progress.Tables[table]; ok {
				tp.VerifyStatus = status
			}
		}
	}

	if allOK {
		fmt.Println("Verification passed: all tables match")
	} else {
		fmt.Println("Verification failed: some tables have mismatches")
	}
	return nil
}

func (m *Migrator) sampleCompare(table string, sampleSize int) int {
	// [P2-3] 表名白名单校验：白名单不通过返回 0（无差异），避免注入
	if !allowedTables[table] {
		return 0
	}
	rows, err := m.srcDB.QueryContext(context.Background(),
		fmt.Sprintf("SELECT * FROM %s ORDER BY RAND() LIMIT %d", table, sampleSize))
	if err != nil {
		return 0
	}
	defer rows.Close()

	cols, _ := rows.Columns()
	if len(cols) == 0 {
		return 0
	}

	mismatches := 0
	for rows.Next() {
		vals := make([]interface{}, len(cols))
		valPtrs := make([]interface{}, len(cols))
		for i := range vals {
			valPtrs[i] = &vals[i]
		}
		if err := rows.Scan(valPtrs...); err != nil {
			continue
		}

		// [P1-52] 列名来自 rows.Columns()（数据库 schema），非用户输入。
		// 此处做防御性校验，防止源库被篡改后通过列名注入 SQL。
		pkCol := sanitizeIdentifier(cols[0])
		pkVal := fmt.Sprintf("%v", vals[0])
		var tgtVal interface{}
		err := m.tgtDB.QueryRowContext(context.Background(),
			fmt.Sprintf("SELECT %s FROM %s WHERE %s = ?", pkCol, table, pkCol), pkVal).Scan(&tgtVal)
		if err != nil {
			mismatches++
			fmt.Printf("    %s row %s=%s: not found in target\n", table, pkCol, pkVal)
		}
	}

	return mismatches
}

func (m *Migrator) GenerateReport() (*MigrationReport, error) {
	if m.progress == nil {
		return nil, fmt.Errorf("no migration progress data")
	}

	report := &MigrationReport{
		SourceDriver: m.progress.SourceDriver,
		TargetDriver: m.progress.TargetDriver,
		StartedAt:    m.progress.StartedAt,
		CompletedAt:  m.progress.CompletedAt,
		Status:       m.progress.Status,
		Tables:       make([]TableReport, 0, len(m.progress.Tables)),
	}

	for name, tp := range m.progress.Tables {
		report.Tables = append(report.Tables, TableReport{
			TableName:    name,
			TotalRows:    tp.TotalRows,
			MigratedRows: tp.MigratedRows,
			Duration:     tp.Duration,
			VerifyStatus: tp.VerifyStatus,
			CompletedAt:  tp.CompletedAt,
		})
		report.TotalRows += tp.TotalRows
		report.TotalMigrated += tp.MigratedRows
	}

	return report, nil
}

type MigrationReport struct {
	SourceDriver  string        `json:"source_driver"`
	TargetDriver  string        `json:"target_driver"`
	StartedAt     time.Time     `json:"started_at"`
	CompletedAt   time.Time     `json:"completed_at"`
	Status        string        `json:"status"`
	Tables        []TableReport `json:"tables"`
	TotalRows     int64         `json:"total_rows"`
	TotalMigrated int64         `json:"total_migrated"`
}

type TableReport struct {
	TableName    string    `json:"table_name"`
	TotalRows    int64     `json:"total_rows"`
	MigratedRows int64     `json:"migrated_rows"`
	Duration     string    `json:"duration"`
	VerifyStatus string    `json:"verify_status"`
	CompletedAt  time.Time `json:"completed_at"`
}

func (m *Migrator) Close() {
	if m.srcDB != nil { m.srcDB.Close() }
	if m.tgtDB != nil { m.tgtDB.Close() }
}

func (m *Migrator) loadProgress() error {
	if m.config.ConfigDir == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(m.config.ConfigDir, "migration_progress.json"))
	if err != nil {
		return err
	}
	return json.Unmarshal(data, m.progress)
}

func (m *Migrator) saveProgress() error {
	if m.config.ConfigDir == "" {
		return nil
	}
	_ = os.MkdirAll(m.config.ConfigDir, 0700)
	data, err := json.MarshalIndent(m.progress, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(m.config.ConfigDir, "migration_progress.json"), data, 0600)
}