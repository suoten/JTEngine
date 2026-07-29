package migration

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"go.uber.org/zap"
)

// ===================================================================
// SchemaVersionManager 数据库 Schema 版本管理器
//
// 实现类 golang-migrate 的版本管理机制，但零外部依赖：
//   1. 维护 schema_versions 表记录已执行的迁移版本
//   2. 支持 Up（升级）和 Down（回滚）操作
//   3. 支持版本跳转和状态查询
//
// 迁移文件命名规范：{version}_{description}.sql
//   正向迁移：001_init.sql, 002_add_users.sql
//   回滚迁移：001_init.down.sql, 002_add_users.down.sql
//
// schema_versions 表结构（自动创建）：
//   CREATE TABLE schema_versions (
//       version    VARCHAR(255) PRIMARY KEY,  -- 版本号（文件名前缀）
//       applied_at TIMESTAMP NOT NULL,         -- 执行时间
//       description TEXT                       -- 描述（文件名后缀）
//   );
// ===================================================================

// Migration 单个迁移定义
type Migration struct {
	Version     string // 版本号（如 "001", "002"）
	Description string // 描述（如 "init", "add_users"）
	UpSQL       string // 正向迁移 SQL
	DownSQL     string // 回滚 SQL（可选，空表示不可回滚）
}

// SchemaVersionManager Schema 版本管理器
type SchemaVersionManager struct {
	db         *sql.DB
	logger     *zap.Logger
	migrations []Migration // 注册的迁移列表（按版本号排序）
}

// NewSchemaVersionManager 创建 Schema 版本管理器
func NewSchemaVersionManager(db *sql.DB, logger *zap.Logger) *SchemaVersionManager {
	return &SchemaVersionManager{
		db:     db,
		logger: logger,
	}
}

// RegisterMigration 注册一个迁移
func (m *SchemaVersionManager) RegisterMigration(mig Migration) {
	m.migrations = append(m.migrations, mig)
	// 保持按版本号排序
	sort.Slice(m.migrations, func(i, j int) bool {
		return m.migrations[i].Version < m.migrations[j].Version
	})
}

// ensureVersionsTable 确保 schema_versions 表存在
func (m *SchemaVersionManager) ensureVersionsTable(ctx context.Context) error {
	_, err := m.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_versions (
		version     VARCHAR(255) PRIMARY KEY,
		applied_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		description TEXT
	)`)
	if err != nil {
		return fmt.Errorf("create schema_versions table: %w", err)
	}
	return nil
}

// CurrentVersion 返回当前已应用的最高版本号
// 返回空字符串表示没有任何迁移被应用
func (m *SchemaVersionManager) CurrentVersion(ctx context.Context) (string, error) {
	if err := m.ensureVersionsTable(ctx); err != nil {
		return "", err
	}
	var version string
	err := m.db.QueryRowContext(ctx,
		"SELECT version FROM schema_versions ORDER BY version DESC LIMIT 1").Scan(&version)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil // 没有迁移记录
		}
		return "", fmt.Errorf("query current version: %w", err)
	}
	return version, nil
}

// AppliedVersions 返回所有已应用的版本号列表（按版本号升序）
func (m *SchemaVersionManager) AppliedVersions(ctx context.Context) ([]string, error) {
	if err := m.ensureVersionsTable(ctx); err != nil {
		return nil, err
	}
	rows, err := m.db.QueryContext(ctx,
		"SELECT version FROM schema_versions ORDER BY version ASC")
	if err != nil {
		return nil, fmt.Errorf("query applied versions: %w", err)
	}
	defer rows.Close()

	var versions []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		versions = append(versions, v)
	}
	return versions, rows.Err()
}

// Up 执行所有未应用的迁移（正向）
// 返回已应用的迁移数量
func (m *SchemaVersionManager) Up(ctx context.Context) (int, error) {
	if err := m.ensureVersionsTable(ctx); err != nil {
		return 0, err
	}

	applied, err := m.AppliedVersions(ctx)
	if err != nil {
		return 0, err
	}
	appliedSet := make(map[string]bool, len(applied))
	for _, v := range applied {
		appliedSet[v] = true
	}

	count := 0
	for _, mig := range m.migrations {
		if appliedSet[mig.Version] {
			continue // 已应用，跳过
		}

		if err := m.applyMigration(ctx, mig); err != nil {
			return count, fmt.Errorf("apply migration %s: %w", mig.Version, err)
		}
		count++
		m.logger.Info("migration applied (up)",
			zap.String("version", mig.Version),
			zap.String("description", mig.Description))
	}

	if count == 0 {
		m.logger.Info("no pending migrations, schema is up to date")
	} else {
		m.logger.Info("migrations completed", zap.Int("applied", count))
	}
	return count, nil
}

// Down 回滚指定数量的迁移（从最新版本开始倒序回滚）
// steps=1 回滚最近一个版本，steps=-1 回滚所有版本
func (m *SchemaVersionManager) Down(ctx context.Context, steps int) (int, error) {
	if err := m.ensureVersionsTable(ctx); err != nil {
		return 0, err
	}

	applied, err := m.AppliedVersions(ctx)
	if err != nil {
		return 0, err
	}
	if len(applied) == 0 {
		m.logger.Info("no migrations to roll back")
		return 0, nil
	}

	// 倒序排列已应用版本
	sort.Sort(sort.Reverse(sort.StringSlice(applied)))

	if steps < 0 || steps > len(applied) {
		steps = len(applied) // 回滚全部
	}

	// 构建 version -> Migration 映射
	migMap := make(map[string]Migration, len(m.migrations))
	for _, mig := range m.migrations {
		migMap[mig.Version] = mig
	}

	count := 0
	for i := 0; i < steps; i++ {
		version := applied[i]
		mig, ok := migMap[version]
		if !ok {
			return count, fmt.Errorf("migration %s not found in registry", version)
		}
		if mig.DownSQL == "" {
			return count, fmt.Errorf("migration %s has no down SQL (irreversible)", version)
		}

		if err := m.rollbackMigration(ctx, mig); err != nil {
			return count, fmt.Errorf("rollback migration %s: %w", mig.Version, err)
		}
		count++
		m.logger.Info("migration rolled back (down)",
			zap.String("version", mig.Version),
			zap.String("description", mig.Description))
	}

	return count, nil
}

// applyMigration 执行单个正向迁移（事务包裹）
func (m *SchemaVersionManager) applyMigration(ctx context.Context, mig Migration) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// 执行迁移 SQL（可能包含多条语句）
	if err := execMultiStatement(ctx, tx, mig.UpSQL); err != nil {
		return fmt.Errorf("exec migration sql: %w", err)
	}

	// 记录版本
	_, err = tx.ExecContext(ctx,
		"INSERT INTO schema_versions (version, applied_at, description) VALUES (?, ?, ?)",
		mig.Version, time.Now().UTC(), mig.Description)
	if err != nil {
		return fmt.Errorf("record version: %w", err)
	}

	return tx.Commit()
}

// rollbackMigration 执行单个回滚迁移（事务包裹）
func (m *SchemaVersionManager) rollbackMigration(ctx context.Context, mig Migration) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// 执行回滚 SQL
	if err := execMultiStatement(ctx, tx, mig.DownSQL); err != nil {
		return fmt.Errorf("exec rollback sql: %w", err)
	}

	// 删除版本记录
	_, err = tx.ExecContext(ctx,
		"DELETE FROM schema_versions WHERE version = ?", mig.Version)
	if err != nil {
		return fmt.Errorf("delete version record: %w", err)
	}

	return tx.Commit()
}

// execMultiStatement 执行可能包含多条语句的 SQL
// 按分号拆分后逐条执行，跳过空语句
func execMultiStatement(ctx context.Context, tx *sql.Tx, sqlText string) error {
	// 简单按分号拆分（不处理存储过程等复杂场景）
	stmts := strings.Split(sqlText, ";")
	for _, stmt := range stmts {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("exec statement [%s...]: %w", truncate(stmt, 50), err)
		}
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
