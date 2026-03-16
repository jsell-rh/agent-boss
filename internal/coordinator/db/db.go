// Package db provides the database layer for agent-boss using GORM.
// Supports SQLite (default) and PostgreSQL via environment variables.
//
// Environment variables:
//
//	DB_TYPE    sqlite|postgres (default: sqlite)
//	DB_PATH    path to SQLite file (default: $DATA_DIR/boss.db)
//	DB_DSN     full DSN for postgres (e.g. host=... user=... dbname=... sslmode=disable)
package db

import (
	"fmt"
	"os"
	"strings"

	glebarez "github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Open initialises the database connection and runs auto-migrations.
// dataDir is used to derive the default SQLite path when DB_PATH is not set.
func Open(dataDir string) (*gorm.DB, error) {
	dbType := os.Getenv("DB_TYPE")
	if dbType == "" {
		dbType = "sqlite"
	}

	cfg := &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	}

	var db *gorm.DB
	var err error

	switch dbType {
	case "sqlite":
		dbPath := os.Getenv("DB_PATH")
		if dbPath == "" {
			dbPath = dataDir + "/boss.db"
		}
		db, err = gorm.Open(glebarez.Open(dbPath+"?_journal_mode=WAL&_synchronous=NORMAL&_foreign_keys=on"), cfg)
		if err != nil {
			return nil, fmt.Errorf("open sqlite %q: %w", dbPath, err)
		}
		// SQLite: single writer, many readers — use connection pool of 1 writer.
		sqlDB, _ := db.DB()
		sqlDB.SetMaxOpenConns(1)

	case "postgres":
		dsn := os.Getenv("DB_DSN")
		if dsn == "" {
			return nil, fmt.Errorf("DB_TYPE=postgres requires DB_DSN to be set")
		}
		db, err = gorm.Open(postgres.Open(dsn), cfg)
		if err != nil {
			return nil, fmt.Errorf("open postgres: %w", err)
		}

	default:
		return nil, fmt.Errorf("unsupported DB_TYPE %q: must be sqlite or postgres", dbType)
	}

	if err := migrate(db); err != nil {
		return nil, fmt.Errorf("auto-migrate: %w", err)
	}

	return db, nil
}

// migrate runs GORM AutoMigrate for all models. Safe to call on every startup —
// it only adds missing tables/columns; it never drops or alters existing data.
func migrate(db *gorm.DB) error {
	// Run manual migration before AutoMigrate so schema is correct before GORM
	// inspects it.
	if err := migrateTasksCompositeKey(db); err != nil {
		return fmt.Errorf("migrate tasks composite key: %w", err)
	}
	if err := db.AutoMigrate(
		&Space{},
		&Agent{},
		&AgentMessage{},
		&AgentNotification{},
		&Task{},
		&TaskComment{},
		&TaskEvent{},
		&StatusSnapshot{},
		&Setting{},
		&SpaceEventLog{},
		&InterruptRecord{},
		&PersonaRow{},
		&PersonaVersionRow{},
	); err != nil {
		return err
	}

	// One-time migration: copy tmux_session → session_id for existing rows,
	// then drop the obsolete column.
	if db.Migrator().HasColumn(&Agent{}, "tmux_session") {
		db.Exec(`UPDATE agents SET session_id = tmux_session WHERE (session_id IS NULL OR session_id = '') AND tmux_session != ''`)
		db.Migrator().DropColumn(&Agent{}, "tmux_session")
	}

	return nil
}

// migrateTasksTable ensures the tasks table has:
//  1. A composite primary key (space_name, id) — fixes cross-space collisions.
//  2. Backtick-quoted column names in the DDL — required so GORM's SQLite
//     schema parser can recognise every column during later AutoMigrate runs.
//
// SQLite stores the original CREATE TABLE text in sqlite_master. If that DDL
// uses unquoted column names (from an earlier raw-SQL migration), GORM fails
// to parse some columns and omits them during table-recreation migrations,
// leading to NOT NULL constraint failures.
//
// The migration is idempotent: it checks the stored DDL and only recreates
// the table when the schema needs fixing.
func migrateTasksCompositeKey(db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}

	// Check whether the tasks table exists at all. If not, AutoMigrate will
	// create it fresh with the correct schema — nothing to do here.
	var tableCount int
	row := sqlDB.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='tasks'`)
	if err := row.Scan(&tableCount); err != nil || tableCount == 0 {
		return nil
	}

	// Check if the DDL already uses backtick-quoted columns (GORM-compatible).
	var ddl string
	row = sqlDB.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='tasks'`)
	if err := row.Scan(&ddl); err != nil {
		return err
	}
	// If the DDL contains backtick-quoted column names, it's already GORM-compatible.
	if strings.Contains(ddl, "`id`") && strings.Contains(ddl, "`space_name`") {
		return nil
	}

	// Recreate the table with GORM-compatible DDL (backtick-quoted columns,
	// composite PK). Use the standard SQLite table-recreation pattern.
	const recreateSQL = "CREATE TABLE `tasks_new` (`id` TEXT NOT NULL,`space_name` TEXT NOT NULL,`title` TEXT NOT NULL,`description` TEXT,`status` TEXT NOT NULL DEFAULT 'backlog',`priority` TEXT DEFAULT 'medium',`assigned_to` TEXT,`created_by` TEXT NOT NULL,`labels` TEXT,`parent_task` TEXT,`subtasks` TEXT,`linked_branch` TEXT,`linked_pr` TEXT,`created_at` DATETIME,`updated_at` DATETIME,`due_at` DATETIME,PRIMARY KEY (`space_name`,`id`));" +
		"INSERT OR IGNORE INTO `tasks_new` SELECT `id`,`space_name`,`title`,`description`,`status`,`priority`,`assigned_to`,`created_by`,`labels`,`parent_task`,`subtasks`,`linked_branch`,`linked_pr`,`created_at`,`updated_at`,`due_at` FROM `tasks`;" +
		"DROP TABLE `tasks`;" +
		"ALTER TABLE `tasks_new` RENAME TO `tasks`;"

	_, err = sqlDB.Exec(recreateSQL)
	return err
}
