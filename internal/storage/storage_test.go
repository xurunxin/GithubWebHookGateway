package storage

import (
	"os"
	"testing"
	"time"
)

func setupTestDB(t *testing.T) *DB {
	t.Helper()

	path := t.TempDir() + "/test.db"
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	wd, _ := os.Getwd()
	migrationPath := wd + "/../../migrations/001_init.sql"
	if _, err := os.Stat(migrationPath); os.IsNotExist(err) {
		migrationPath = wd + "/migrations/001_init.sql"
	}

	if err := db.Migrate(migrationPath); err != nil {
		db.Close()
		t.Fatal(err)
	}

	return db
}

func TestInsertAndGetEvent(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	e := &Event{
		DeliveryID:         "del-001",
		GitHubEvent:        "push",
		RepositoryFullName: "owner/repo",
		InstallationID:     12345,
		PayloadJSON:        `{"ref":"refs/heads/main"}`,
	}

	if err := db.InsertEvent(e); err != nil {
		t.Fatal(err)
	}

	if e.ID == 0 {
		t.Fatal("expected non-zero ID after insert")
	}

	retrieved, err := db.GetEventByDeliveryID("del-001")
	if err != nil {
		t.Fatal(err)
	}
	if retrieved == nil {
		t.Fatal("expected event, got nil")
	}
	if retrieved.DeliveryID != "del-001" {
		t.Fatalf("expected del-001, got %s", retrieved.DeliveryID)
	}
	if retrieved.Status != "pending" {
		t.Fatalf("expected pending status, got %s", retrieved.Status)
	}
}

func TestDuplicateDeliveryID(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	e1 := &Event{
		DeliveryID:  "del-dup",
		GitHubEvent: "push",
		PayloadJSON: `{"ref":"main"}`,
	}
	if err := db.InsertEvent(e1); err != nil {
		t.Fatal(err)
	}

	e2 := &Event{
		DeliveryID:  "del-dup",
		GitHubEvent: "push",
		PayloadJSON: `{"ref":"main"}`,
	}
	err := db.InsertEvent(e2)
	if err == nil {
		t.Fatal("expected duplicate key error")
	}
}

func TestUpdateEventStatus_Acked(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	e := &Event{
		DeliveryID:  "del-ack-001",
		GitHubEvent: "pull_request",
		PayloadJSON: `{}`,
	}
	db.InsertEvent(e)

	err := db.UpdateEventStatus(e.ID, EventStatusAcked, "", false, 10, 5, 300)
	if err != nil {
		t.Fatal(err)
	}

	updated, _ := db.GetEventByID(e.ID)
	if updated.Status != string(EventStatusAcked) {
		t.Fatalf("expected acked, got %s", updated.Status)
	}
	if updated.AckedAt == nil {
		t.Fatal("expected acked_at to be set")
	}
}

func TestUpdateEventStatus_Failed_Retryable(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	e := &Event{
		DeliveryID:  "del-retry-001",
		GitHubEvent: "issues",
		PayloadJSON: `{}`,
	}
	db.InsertEvent(e)

	err := db.UpdateEventStatus(e.ID, EventStatusFailed, "timeout", true, 10, 5, 300)
	if err != nil {
		t.Fatal(err)
	}

	updated, _ := db.GetEventByID(e.ID)
	if updated.Status != string(EventStatusPending) {
		t.Fatalf("expected pending for retry, got %s", updated.Status)
	}
	if updated.RetryCount != 1 {
		t.Fatalf("expected retry_count=1, got %d", updated.RetryCount)
	}
	if updated.NextRetryAt == nil {
		t.Fatal("expected next_retry_at")
	}
}

func TestUpdateEventStatus_Failed_NonRetryable(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	e := &Event{
		DeliveryID:  "del-dead-001",
		GitHubEvent: "gollum",
		PayloadJSON: `{}`,
	}
	db.InsertEvent(e)

	err := db.UpdateEventStatus(e.ID, EventStatusFailed, "unsupported", false, 10, 5, 300)
	if err != nil {
		t.Fatal(err)
	}

	updated, _ := db.GetEventByID(e.ID)
	if updated.Status != string(EventStatusDead) {
		t.Fatalf("expected dead, got %s", updated.Status)
	}
}

func TestUpdateEventStatus_MaxRetriesExceeded(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	e := &Event{
		DeliveryID:  "del-maxretry",
		GitHubEvent: "issues",
		PayloadJSON: `{}`,
	}
	db.InsertEvent(e)

	for i := 0; i < 5; i++ {
		db.UpdateEventStatus(e.ID, EventStatusFailed, "test error", true, 3, 1, 300)
	}

	updated, _ := db.GetEventByID(e.ID)
	if updated.Status != string(EventStatusDead) {
		t.Fatalf("expected dead after max retries, got %s", updated.Status)
	}
}

func TestRetryEvent(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	e := &Event{
		DeliveryID:  "del-replay",
		GitHubEvent: "issues",
		PayloadJSON: `{}`,
	}
	db.InsertEvent(e)

	db.UpdateEventStatus(e.ID, EventStatusFailed, "error", false, 10, 5, 300)

	if err := db.RetryEvent(e.ID); err != nil {
		t.Fatal(err)
	}

	updated, _ := db.GetEventByID(e.ID)
	if updated.Status != string(EventStatusPending) {
		t.Fatalf("expected pending after retry, got %s", updated.Status)
	}
	if updated.LastError != "" {
		t.Fatalf("expected empty last_error, got %s", updated.LastError)
	}
}

func TestGetPendingEvents(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	for i := 0; i < 5; i++ {
		e := &Event{
			DeliveryID:  "del-pending-" + string(rune('0'+i)),
			GitHubEvent: "push",
			PayloadJSON: `{}`,
		}
		db.InsertEvent(e)
	}

	events, err := db.GetPendingEvents(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 5 {
		t.Fatalf("expected 5 pending events, got %d", len(events))
	}
}

func TestGetStatusCounts(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	e1 := &Event{DeliveryID: "del-s1", GitHubEvent: "push", PayloadJSON: `{}`}
	e2 := &Event{DeliveryID: "del-s2", GitHubEvent: "push", PayloadJSON: `{}`}
	e3 := &Event{DeliveryID: "del-s3", GitHubEvent: "push", PayloadJSON: `{}`}

	db.InsertEvent(e1)
	db.InsertEvent(e2)
	db.InsertEvent(e3)

	db.UpdateEventStatus(e1.ID, EventStatusAcked, "", false, 10, 5, 300)

	counts, err := db.GetStatusCounts()
	if err != nil {
		t.Fatal(err)
	}
	if counts.Pending != 2 {
		t.Fatalf("expected 2 pending, got %d", counts.Pending)
	}
	if counts.Acked != 1 {
		t.Fatalf("expected 1 acked, got %d", counts.Acked)
	}
	if counts.Total != 3 {
		t.Fatalf("expected 3 total, got %d", counts.Total)
	}
}

func TestUpsertAndDisconnectAgent(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	if err := db.UpsertAgent("agent-01"); err != nil {
		t.Fatal(err)
	}

	agent, err := db.GetAgent("agent-01")
	if err != nil {
		t.Fatal(err)
	}
	if !agent.Connected {
		t.Fatal("expected agent connected")
	}

	if err := db.DisconnectAgent("agent-01"); err != nil {
		t.Fatal(err)
	}

	agent, _ = db.GetAgent("agent-01")
	if agent.Connected {
		t.Fatal("expected agent disconnected")
	}
}

func TestWALMode(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	var journalMode string
	err := db.conn.QueryRow("PRAGMA journal_mode").Scan(&journalMode)
	if err != nil {
		t.Fatal(err)
	}

	if journalMode != "wal" {
		t.Fatalf("expected wal, got %s", journalMode)
	}
}

func TestExponentialBackoff(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	e := &Event{
		DeliveryID:  "del-backoff",
		GitHubEvent: "push",
		PayloadJSON: `{}`,
	}
	db.InsertEvent(e)

	db.UpdateEventStatus(e.ID, EventStatusFailed, "e1", true, 10, 2, 60)

	e1, _ := db.GetEventByID(e.ID)
	delay1 := e1.NextRetryAt.Sub(time.Now().UTC())

	db.UpdateEventStatus(e.ID, EventStatusFailed, "e2", true, 10, 2, 60)

	e2, _ := db.GetEventByID(e.ID)
	delay2 := e2.NextRetryAt.Sub(time.Now().UTC())

	if delay2 <= delay1 {
		t.Fatalf("expected increasing backoff: d1=%v d2=%v", delay1, delay2)
	}
}
