// jte-migrate 数据迁移 CLI 工具
//
// AUTO-FIX-2026-07-16 [P2]: 规划 Section 9.11 要求 SQLite→四层存储迁移工具
// 功能：
//   - SQLite → MySQL/PostgreSQL 关系层迁移
//   - 断点续传（进度持久化到 migration_progress.json）
//   - 数据校验（行数对比 + 采样比对）
//   - 干运行模式（DryRun 仅统计不写入）
//   - 迁移报告生成（JSON 格式）
//
// 用法：
//   jte-migrate --source sqlite3:data/jte.db --target mysql:user:pass@tcp(host:3306)/jte --dry-run
//   jte-migrate --source sqlite3:data/jte.db --target mysql:user:pass@tcp(host:3306)/jte
//   jte-migrate --source sqlite3:data/jte.db --target mysql:user:pass@tcp(host:3306)/jte --verify
//   jte-migrate --source sqlite3:data/jte.db --target mysql:user:pass@tcp(host:3306)/jte --report
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/suoten/jt-engine/internal/migration"
	"go.uber.org/zap"
)

func main() {
	var (
		sourceDSN  string
		targetDSN  string
		sourceDrv  string
		targetDrv  string
		batchSize  int
		dryRun     bool
		verifyOnly bool
		reportOnly bool
		configDir  string
	)

	flag.StringVar(&sourceDrv, "source-driver", "sqlite3", "source database driver (sqlite3/mysql/postgres)")
	flag.StringVar(&sourceDSN, "source", "", "source DSN (e.g. data/jte.db for sqlite3)")
	flag.StringVar(&targetDrv, "target-driver", "mysql", "target database driver (mysql/postgres)")
	flag.StringVar(&targetDSN, "target", "", "target DSN (e.g. user:pass@tcp(host:3306)/jte)")
	flag.IntVar(&batchSize, "batch", 1000, "batch size for migration")
	flag.BoolVar(&dryRun, "dry-run", false, "dry run mode: count rows without writing")
	flag.BoolVar(&verifyOnly, "verify", false, "verify only: compare source and target row counts")
	flag.BoolVar(&reportOnly, "report", false, "generate migration report from saved progress")
	flag.StringVar(&configDir, "config-dir", ".", "directory for progress file storage")

	flag.Parse()

	if sourceDSN == "" && !reportOnly {
		fmt.Fprintln(os.Stderr, "Error: --source is required")
		flag.Usage()
		os.Exit(1)
	}
	if targetDSN == "" && !reportOnly && !dryRun {
		fmt.Fprintln(os.Stderr, "Error: --target is required")
		flag.Usage()
		os.Exit(1)
	}

	// 初始化 logger
	logger, _ := zap.NewProduction()
	defer logger.Sync()

	cfg := &migration.MigratorConfig{
		SourceDriver: sourceDrv,
		SourceDSN:    sourceDSN,
		TargetDriver: targetDrv,
		TargetDSN:    targetDSN,
		BatchSize:    batchSize,
		DryRun:       dryRun,
		ConfigDir:    configDir,
	}

	migrator := migration.NewMigrator(cfg, logger)

	// 仅生成报告模式
	if reportOnly {
		if err := migrator.Connect(); err != nil {
			// 连接失败时尝试从进度文件加载
			logger.Warn("connect failed, trying to load progress file only", zap.Error(err))
		}
		report, err := migrator.GenerateReport()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error generating report: %v\n", err)
			os.Exit(1)
		}
		printReport(report)
		return
	}

	// 连接数据库
	if err := migrator.Connect(); err != nil {
		fmt.Fprintf(os.Stderr, "Error connecting databases: %v\n", err)
		os.Exit(1)
	}
	defer migrator.Close()

	// 仅校验模式
	if verifyOnly {
		fmt.Println("=== Verification Mode ===")
		if err := migrator.Verify(); err != nil {
			fmt.Fprintf(os.Stderr, "Verification error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// 执行迁移
	if dryRun {
		fmt.Println("=== Dry Run Mode ===")
	}

	start := time.Now()
	if err := migrator.Migrate(); err != nil {
		fmt.Fprintf(os.Stderr, "Migration failed: %v\n", err)
		os.Exit(1)
	}
	elapsed := time.Since(start)

	fmt.Printf("\n=== Migration Completed in %s ===\n", elapsed)

	// 自动校验
	if !dryRun {
		fmt.Println("\n=== Auto Verification ===")
		if err := migrator.Verify(); err != nil {
			fmt.Fprintf(os.Stderr, "Verification error: %v\n", err)
		}
	}

	// 生成报告
	report, err := migrator.GenerateReport()
	if err == nil {
		printReport(report)
		// 保存 JSON 报告
		reportFile := fmt.Sprintf("migration_report_%s.json", time.Now().Format("20060102_150405"))
		if data, err := json.MarshalIndent(report, "", "  "); err == nil {
			if err := os.WriteFile(reportFile, data, 0644); err == nil {
				fmt.Printf("\nReport saved to: %s\n", reportFile)
			}
		}
	}
}

func printReport(report *migration.MigrationReport) {
	fmt.Println("\n=== Migration Report ===")
	fmt.Printf("Source: %s\n", report.SourceDriver)
	fmt.Printf("Target: %s\n", report.TargetDriver)
	fmt.Printf("Status: %s\n", report.Status)
	fmt.Printf("Started:  %s\n", report.StartedAt.Format("2006-01-02 15:04:05"))
	if !report.CompletedAt.IsZero() {
		fmt.Printf("Completed: %s\n", report.CompletedAt.Format("2006-01-02 15:04:05"))
	}
	fmt.Printf("Total Rows: %d\n", report.TotalRows)
	fmt.Printf("Migrated:   %d\n", report.TotalMigrated)

	fmt.Println("\nTable Details:")
	fmt.Printf("  %-20s %-12s %-12s %-12s %s\n", "Table", "Total", "Migrated", "Duration", "Verify")
	fmt.Println("  " + repeat("-", 70))
	for _, t := range report.Tables {
		dur := t.Duration
		if dur == "" {
			dur = "-"
		}
		verify := t.VerifyStatus
		if verify == "" {
			verify = "-"
		}
		fmt.Printf("  %-20s %-12d %-12d %-12s %s\n",
			t.TableName, t.TotalRows, t.MigratedRows, dur, verify)
	}
}

func repeat(s string, n int) string {
	result := ""
	for i := 0; i < n; i++ {
		result += s
	}
	return result
}
