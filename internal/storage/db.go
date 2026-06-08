package storage

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type DB struct {
	conn *sql.DB
	path string
}

func Open(dbPath string) (*DB, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create db directory: %w", err)
	}

	dsn := fmt.Sprintf("%s?_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=5000&_foreign_keys=on", dbPath)

	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	conn.SetMaxOpenConns(2)
	conn.SetMaxIdleConns(1)
	conn.SetConnMaxLifetime(time.Hour)

	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("ping database: %w", err)
	}

	log.Printf("[db] sqlite opened: %s (wal mode)", dbPath)
	return &DB{conn: conn, path: dbPath}, nil
}

func (db *DB) Close() error {
	return db.conn.Close()
}

func (db *DB) Conn() *sql.DB {
	return db.conn
}

func (db *DB) Migrate(sqlPath string) error {
	data, err := os.ReadFile(sqlPath)
	if err != nil {
		return fmt.Errorf("read migration file: %w", err)
	}

	_, err = db.conn.Exec(string(data))
	if err != nil {
		return fmt.Errorf("execute migration: %w", err)
	}

	log.Printf("[db] migrations applied from: %s", sqlPath)
	return nil
}
