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
	// Task is excluded from AutoMigrate: GORM's SQLite dialect does not handle
	// composite primary keys correctly during auto-migration — it tries to
	// recreate via tasks__temp and fails with NOT NULL constraint errors.
	// migrateTasksCompositeKey manages the tasks table schema entirely (both
	// fresh creation and migration from the old single-column PK).
	if err := migrateTasksCompositeKey(db); err != nil {
		return fmt.Errorf("migrate tasks composite key: %w", err)
	}
	return db.AutoMigrate(
		&Space{},
		&Agent{},
		&AgentMessage{},
		&AgentNotification{},
		&TaskComment{},
		&TaskEvent{},
		&StatusSnapshot{},
	)
}

// migrateTasksCompositeKey recreates the tasks table with a composite primary
// key (space_name, id) if it currently has a single-column PK on id only.
// This fixes cross-space task ID collisions: tasks with the same sequence
// number (e.g. TASK-001) in different spaces were overwriting each other.
//
// SQLite does not support ALTER TABLE … DROP PRIMARY KEY, so we use the
// standard SQLite table-recreation pattern: create new → copy → drop → rename.
// The migration is idempotent: if the table already has the correct schema
// (detected by checking the primary key pragma), it is a no-op.
func migrateTasksCompositeKey(db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}

	// Check whether the tasks table exists. If not, create it fresh with the
	// correct composite PK. (Task is excluded from AutoMigrate, so we must
	// handle initial creation here too.)
	var tableCount int
	row := sqlDB.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='tasks'`)
	if err := row.Scan(&tableCount); err != nil || tableCount == 0 {
		_, err := sqlDB.Exec(`
			CREATE TABLE IF NOT EXISTS tasks (
				id            TEXT NOT NULL,
				space_name    TEXT NOT NULL,
				title         TEXT NOT NULL,
				description   TEXT,
				status        TEXT NOT NULL DEFAULT 'backlog',
				priority      TEXT DEFAULT 'medium',
				assigned_to   TEXT,
				created_by    TEXT NOT NULL,
				labels        TEXT,
				parent_task   TEXT,
				subtasks      TEXT,
				linked_branch TEXT,
				linked_pr     TEXT,
				created_at    DATETIME,
				updated_at    DATETIME,
				due_at        DATETIME,
				PRIMARY KEY (space_name, id)
			)
		`)
		return err
	}

	// Inspect current primary key columns via the table_info pragma.
	// SQLite returns pk>0 for primary-key columns; composite PKs have pk=1,2,...
	rows, err := sqlDB.Query(`PRAGMA table_info(tasks)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type colInfo struct {
		cid     int
		name    string
		typ     string
		notnull int
		dflt    interface{}
		pk      int
	}
	var pkCols []string
	for rows.Next() {
		var c colInfo
		if err := rows.Scan(&c.cid, &c.name, &c.typ, &c.notnull, &c.dflt, &c.pk); err != nil {
			return err
		}
		if c.pk > 0 {
			pkCols = append(pkCols, c.name)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	// Already has composite PK — nothing to do.
	if len(pkCols) == 2 {
		return nil
	}

	// Single-column PK (old schema): recreate with composite (space_name, id).
	_, err = sqlDB.Exec(`
		CREATE TABLE IF NOT EXISTS tasks_new (
			id            TEXT NOT NULL,
			space_name    TEXT NOT NULL,
			title         TEXT NOT NULL,
			description   TEXT,
			status        TEXT NOT NULL DEFAULT 'backlog',
			priority      TEXT DEFAULT 'medium',
			assigned_to   TEXT,
			created_by    TEXT NOT NULL,
			labels        TEXT,
			parent_task   TEXT,
			subtasks      TEXT,
			linked_branch TEXT,
			linked_pr     TEXT,
			created_at    DATETIME,
			updated_at    DATETIME,
			due_at        DATETIME,
			PRIMARY KEY (space_name, id)
		);
		INSERT OR IGNORE INTO tasks_new
			SELECT id, space_name, title, description, status, priority,
			       assigned_to, created_by, labels, parent_task, subtasks,
			       linked_branch, linked_pr, created_at, updated_at, due_at
			FROM tasks;
		DROP TABLE tasks;
		ALTER TABLE tasks_new RENAME TO tasks;
	`)
	return err
}
