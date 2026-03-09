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
	return db.AutoMigrate(
		&Space{},
		&Agent{},
		&AgentMessage{},
		&AgentNotification{},
		&Task{},
		&TaskComment{},
		&TaskEvent{},
		&StatusSnapshot{},
	)
}
