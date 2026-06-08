package storage

import (
	"database/sql"
	"fmt"
	"time"
)

func (db *DB) UpsertAgent(agentID string) error {
	now := time.Now().UTC()

	const query = `
		INSERT INTO agents (agent_id, connected, last_seen_at, created_at, updated_at)
		VALUES (?, 1, ?, ?, ?)
		ON CONFLICT(agent_id) DO UPDATE SET
			connected = 1,
			last_seen_at = excluded.last_seen_at,
			updated_at = excluded.updated_at
	`
	_, err := db.conn.Exec(query, agentID, now, now, now)
	return err
}

func (db *DB) DisconnectAgent(agentID string) error {
	now := time.Now().UTC()

	const query = `
		UPDATE agents SET connected = 0, updated_at = ? WHERE agent_id = ?
	`
	_, err := db.conn.Exec(query, now, agentID)
	return err
}

func (db *DB) UpdateAgentLastAck(agentID string, eventID int64) error {
	now := time.Now().UTC()

	const query = `
		UPDATE agents SET last_ack_event_id = ?, last_seen_at = ?, updated_at = ? WHERE agent_id = ?
	`
	_, err := db.conn.Exec(query, eventID, now, now, agentID)
	return err
}

func (db *DB) GetAgent(agentID string) (*Agent, error) {
	const query = `
		SELECT id, agent_id, connected, last_seen_at, last_ack_event_id, created_at, updated_at
		FROM agents WHERE agent_id = ?
	`

	a := &Agent{}
	var connected int
	var lastSeenAt sql.NullTime
	var lastAckEventID sql.NullInt64

	err := db.conn.QueryRow(query, agentID).Scan(
		&a.ID, &a.AgentID, &connected, &lastSeenAt,
		&lastAckEventID, &a.CreatedAt, &a.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get agent: %w", err)
	}

	a.Connected = connected == 1
	if lastSeenAt.Valid {
		a.LastSeenAt = &lastSeenAt.Time
	}
	if lastAckEventID.Valid {
		a.LastAckEventID = lastAckEventID.Int64
	}

	return a, nil
}

func (db *DB) GetConnectedAgentCount() (int, error) {
	var count int
	err := db.conn.QueryRow(`SELECT COUNT(1) FROM agents WHERE connected = 1`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count connected agents: %w", err)
	}
	return count, nil
}
