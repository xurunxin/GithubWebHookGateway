package storage

import (
	"fmt"
	"time"
)

func (db *DB) InsertEventLog(eventID int64, level, message string) error {
	now := time.Now().UTC()

	const query = `
		INSERT INTO event_logs (event_id, level, message, created_at)
		VALUES (?, ?, ?, ?)
	`

	_, err := db.conn.Exec(query, eventID, level, message, now)
	if err != nil {
		return fmt.Errorf("insert event log: %w", err)
	}
	return nil
}
