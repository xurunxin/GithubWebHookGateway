package storage

import (
	"database/sql"
	"fmt"
	"time"
)

func (db *DB) InsertEvent(e *Event) error {
	now := time.Now().UTC()
	e.ReceivedAt = now
	e.UpdatedAt = now

	const query = `
		INSERT INTO events (delivery_id, github_event, repository_full_name, installation_id,
		                    payload_json, status, retry_count, received_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	res, err := db.conn.Exec(query,
		e.DeliveryID, e.GitHubEvent, e.RepositoryFullName,
		e.InstallationID, e.PayloadJSON, EventStatusPending, 0,
		e.ReceivedAt, e.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert event: %w", err)
	}

	id, _ := res.LastInsertId()
	e.ID = id
	return nil
}

func (db *DB) GetEventByDeliveryID(deliveryID string) (*Event, error) {
	const query = `
		SELECT id, delivery_id, github_event, repository_full_name, installation_id,
		       payload_json, status, retry_count, next_retry_at, last_error,
		       received_at, updated_at, acked_at
		FROM events WHERE delivery_id = ?
	`

	e := &Event{}
	var nextRetryAt, ackedAt sql.NullTime
	var repoName, lastError sql.NullString
	var installationID sql.NullInt64

	err := db.conn.QueryRow(query, deliveryID).Scan(
		&e.ID, &e.DeliveryID, &e.GitHubEvent, &repoName, &installationID,
		&e.PayloadJSON, &e.Status, &e.RetryCount, &nextRetryAt, &lastError,
		&e.ReceivedAt, &e.UpdatedAt, &ackedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get event by delivery_id: %w", err)
	}

	if repoName.Valid {
		e.RepositoryFullName = repoName.String
	}
	if installationID.Valid {
		e.InstallationID = installationID.Int64
	}
	if nextRetryAt.Valid {
		e.NextRetryAt = &nextRetryAt.Time
	}
	if ackedAt.Valid {
		e.AckedAt = &ackedAt.Time
	}
	if lastError.Valid {
		e.LastError = lastError.String
	}

	return e, nil
}

func (db *DB) GetEventByID(id int64) (*Event, error) {
	const query = `
		SELECT id, delivery_id, github_event, repository_full_name, installation_id,
		       payload_json, status, retry_count, next_retry_at, last_error,
		       received_at, updated_at, acked_at
		FROM events WHERE id = ?
	`

	e := &Event{}
	var nextRetryAt, ackedAt sql.NullTime
	var repoName, lastError sql.NullString
	var installationID sql.NullInt64

	err := db.conn.QueryRow(query, id).Scan(
		&e.ID, &e.DeliveryID, &e.GitHubEvent, &repoName, &installationID,
		&e.PayloadJSON, &e.Status, &e.RetryCount, &nextRetryAt, &lastError,
		&e.ReceivedAt, &e.UpdatedAt, &ackedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get event by id: %w", err)
	}

	if repoName.Valid {
		e.RepositoryFullName = repoName.String
	}
	if installationID.Valid {
		e.InstallationID = installationID.Int64
	}
	if nextRetryAt.Valid {
		e.NextRetryAt = &nextRetryAt.Time
	}
	if ackedAt.Valid {
		e.AckedAt = &ackedAt.Time
	}
	if lastError.Valid {
		e.LastError = lastError.String
	}

	return e, nil
}

func (db *DB) GetPendingEvents(batchSize int) ([]*Event, error) {
	now := time.Now().UTC()

	const query = `
		SELECT id, delivery_id, github_event, repository_full_name, installation_id,
		       payload_json, status, retry_count, next_retry_at, last_error,
		       received_at, updated_at, acked_at
		FROM events
		WHERE status = ? AND (next_retry_at IS NULL OR next_retry_at <= ?)
		ORDER BY id ASC
		LIMIT ?
	`

	rows, err := db.conn.Query(query, EventStatusPending, now, batchSize)
	if err != nil {
		return nil, fmt.Errorf("query pending events: %w", err)
	}
	defer rows.Close()

	return scanEvents(rows)
}

func (db *DB) GetEventsByStatus(status string, limit int) ([]*Event, error) {
	const query = `
		SELECT id, delivery_id, github_event, repository_full_name, installation_id,
		       payload_json, status, retry_count, next_retry_at, last_error,
		       received_at, updated_at, acked_at
		FROM events
		WHERE status = ?
		ORDER BY id DESC
		LIMIT ?
	`

	rows, err := db.conn.Query(query, status, limit)
	if err != nil {
		return nil, fmt.Errorf("query events by status: %w", err)
	}
	defer rows.Close()

	return scanEvents(rows)
}

func (db *DB) UpdateEventStatus(eventID int64, status EventStatus, lastError string, retryable bool, maxRetry int, retryInitSecs int, retryMaxSecs int) error {
	now := time.Now().UTC()

	switch status {
	case EventStatusAcked:
		const query = `
			UPDATE events SET status = ?, updated_at = ?, acked_at = ?
			WHERE id = ?
		`
		_, err := db.conn.Exec(query, EventStatusAcked, now, now, eventID)
		return err

	case EventStatusFailed:
		return db.handleFailed(eventID, lastError, retryable, maxRetry, retryInitSecs, retryMaxSecs)

	case EventStatusDelivering:
		const query = `
			UPDATE events SET status = ?, updated_at = ? WHERE id = ?
		`
		_, err := db.conn.Exec(query, EventStatusDelivering, now, eventID)
		return err

	default:
		const query = `
			UPDATE events SET status = ?, last_error = ?, updated_at = ? WHERE id = ?
		`
		_, err := db.conn.Exec(query, string(status), lastError, now, eventID)
		return err
	}
}

func (db *DB) handleFailed(eventID int64, lastError string, retryable bool, maxRetry, retryInitSecs, retryMaxSecs int) error {
	now := time.Now().UTC()

	e, err := db.GetEventByID(eventID)
	if err != nil {
		return err
	}
	if e == nil {
		return fmt.Errorf("event not found: %d", eventID)
	}

	if !retryable {
		const query = `
			UPDATE events SET status = ?, last_error = ?, updated_at = ? WHERE id = ?
		`
		_, err := db.conn.Exec(query, EventStatusDead, lastError, now, eventID)
		return err
	}

	newRetryCount := e.RetryCount + 1
	if newRetryCount > maxRetry {
		const query = `
			UPDATE events SET status = ?, last_error = ?, retry_count = ?, updated_at = ? WHERE id = ?
		`
		_, err := db.conn.Exec(query, EventStatusDead, lastError, newRetryCount, now, eventID)
		return err
	}

	backoffSecs := retryInitSecs
	for i := 1; i < newRetryCount; i++ {
		backoffSecs *= 2
		if backoffSecs > retryMaxSecs {
			backoffSecs = retryMaxSecs
		}
	}
	nextRetry := now.Add(time.Duration(backoffSecs) * time.Second)

	const query = `
		UPDATE events SET status = ?, retry_count = ?, next_retry_at = ?, last_error = ?, updated_at = ?
		WHERE id = ?
	`
	_, err = db.conn.Exec(query, EventStatusPending, newRetryCount, nextRetry, lastError, now, eventID)
	return err
}

func (db *DB) RetryEvent(eventID int64) error {
	now := time.Now().UTC()

	const query = `
		UPDATE events SET status = ?, next_retry_at = ?, last_error = '', updated_at = ?
		WHERE id = ? AND status IN (?, ?)
	`
	_, err := db.conn.Exec(query, EventStatusPending, now, now, eventID, EventStatusDead, EventStatusFailed)
	return err
}

func (db *DB) GetStatusCounts() (*StatusCounts, error) {
	const query = `
		SELECT
			COALESCE(SUM(CASE WHEN status = 'pending' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'delivering' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'acked' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'dead' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END), 0),
			COUNT(1)
		FROM events
	`

	sc := &StatusCounts{}
	err := db.conn.QueryRow(query).Scan(&sc.Pending, &sc.Delivering, &sc.Acked, &sc.Dead, &sc.Failed, &sc.Total)
	if err != nil {
		return nil, fmt.Errorf("get status counts: %w", err)
	}
	return sc, nil
}

func (db *DB) GetEventsWithPagination(limit, offset int, statusFilter string) ([]*Event, int, error) {
	var total int
	var rows *sql.Rows
	var err error

	if statusFilter != "" {
		err = db.conn.QueryRow(`SELECT COUNT(1) FROM events WHERE status = ?`, statusFilter).Scan(&total)
		if err != nil {
			return nil, 0, fmt.Errorf("count events: %w", err)
		}
		rows, err = db.conn.Query(
			`SELECT id, delivery_id, github_event, repository_full_name, installation_id,
			        payload_json, status, retry_count, next_retry_at, last_error,
			        received_at, updated_at, acked_at
			 FROM events WHERE status = ? ORDER BY id DESC LIMIT ? OFFSET ?`,
			statusFilter, limit, offset,
		)
	} else {
		err = db.conn.QueryRow(`SELECT COUNT(1) FROM events`).Scan(&total)
		if err != nil {
			return nil, 0, fmt.Errorf("count events: %w", err)
		}
		rows, err = db.conn.Query(
			`SELECT id, delivery_id, github_event, repository_full_name, installation_id,
			        payload_json, status, retry_count, next_retry_at, last_error,
			        received_at, updated_at, acked_at
			 FROM events ORDER BY id DESC LIMIT ? OFFSET ?`,
			limit, offset,
		)
	}
	if err != nil {
		return nil, 0, fmt.Errorf("query events: %w", err)
	}
	defer rows.Close()

	events, err := scanEvents(rows)
	if err != nil {
		return nil, 0, err
	}

	return events, total, nil
}

func scanEvents(rows *sql.Rows) ([]*Event, error) {
	var events []*Event

	for rows.Next() {
		e := &Event{}
		var nextRetryAt, ackedAt sql.NullTime
		var repoName, lastError sql.NullString
		var installationID sql.NullInt64

		err := rows.Scan(
			&e.ID, &e.DeliveryID, &e.GitHubEvent, &repoName, &installationID,
			&e.PayloadJSON, &e.Status, &e.RetryCount, &nextRetryAt, &lastError,
			&e.ReceivedAt, &e.UpdatedAt, &ackedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}

		if repoName.Valid {
			e.RepositoryFullName = repoName.String
		}
		if installationID.Valid {
			e.InstallationID = installationID.Int64
		}
		if nextRetryAt.Valid {
			e.NextRetryAt = &nextRetryAt.Time
		}
		if ackedAt.Valid {
			e.AckedAt = &ackedAt.Time
		}
		if lastError.Valid {
			e.LastError = lastError.String
		}

		events = append(events, e)
	}

	return events, rows.Err()
}
