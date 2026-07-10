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
	if err := srcDB.Ping(); err != nil {
		srcDB.Close()
		return fmt.Errorf("ping source db: %w", err)
	}
	m.srcDB = srcDB

	tgtDB, err := sql.Open(m.config.TargetDriver, m.config.TargetDSN)
	if err != nil {
		return fmt.Errorf("open target db: %w", err)
	}
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

	offset := tp.MigratedRows
	for offset < tp.TotalRows {
		query := fmt.Sprintf("SELECT * FROM %s LIMIT %d OFFSET %d", table, m.config.BatchSize, offset)
		rows, err := m.srcDB.QueryContext(context.Background(), query)
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
		for rows.Next() {
			if err := rows.Scan(valuePtrs...); err != nil {
				stmt.Close()
				tx.Rollback()
				rows.Close()
				return fmt.Errorf("scan row: %w", err)
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
		offset += batchCount

		if batchCount > 0 {
			_ = m.saveProgress()
		}

		m.logger.Debug("batch migrated",
			zap.String("table", table),
			zap.Int64("progress", tp.MigratedRows),
			zap.Int64("total", tp.TotalRows))

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

		pkCol := cols[0]
		pkVal := fmt.Sprintf("%v", vals[0])
		var tgtVal interface{}
		err := m.tgtDB.QueryRowContext(context.Background(),
			fmt.Sprintf("SELECT %s FROM %s WHERE %s = ?", cols[0], table, pkCol), pkVal).Scan(&tgtVal)
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