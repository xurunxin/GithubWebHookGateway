package storage

import (
	"testing"
)

// TestGetEventByDeliveryID_NotFound verifies nil is returned for unknown delivery IDs.
func TestGetEventByDeliveryID_NotFound(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	event, err := db.GetEventByDeliveryID("nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if event != nil {
		t.Fatal("expected nil for nonexistent delivery ID")
	}
}

// TestGetEventByID_NotFound verifies nil is returned for unknown event IDs.
func TestGetEventByID_NotFound(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	event, err := db.GetEventByID(99999)
	if err != nil {
		t.Fatal(err)
	}
	if event != nil {
		t.Fatal("expected nil for nonexistent event ID")
	}
}

// TestGetAgent_NotFound verifies nil is returned for unknown agents.
func TestGetAgent_NotFound(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	agent, err := db.GetAgent("ghost-agent")
	if err != nil {
		t.Fatal(err)
	}
	if agent != nil {
		t.Fatal("expected nil for nonexistent agent")
	}
}

// TestUpdateAgentLastAck verifies last_ack_event_id is updated correctly.
func TestUpdateAgentLastAck(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	db.UpsertAgent("agent-ack-track")

	if err := db.UpdateAgentLastAck("agent-ack-track", 42); err != nil {
		t.Fatal(err)
	}

	agent, err := db.GetAgent("agent-ack-track")
	if err != nil {
		t.Fatal(err)
	}
	if agent.LastAckEventID != 42 {
		t.Fatalf("expected last_ack_event_id=42, got %d", agent.LastAckEventID)
	}
}

// TestInsertEventLog verifies an event log entry can be inserted without error.
func TestInsertEventLog(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	e := &Event{DeliveryID: "log-test", GitHubEvent: "push", PayloadJSON: `{}`}
	db.InsertEvent(e)

	if err := db.InsertEventLog(e.ID, "info", "test message"); err != nil {
		t.Fatal(err)
	}
}

// TestGetPendingEvents_Empty verifies empty list when no pending events exist.
func TestGetPendingEvents_Empty(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	e := &Event{DeliveryID: "acked-only", GitHubEvent: "push", PayloadJSON: `{}`}
	db.InsertEvent(e)
	db.UpdateEventStatus(e.ID, EventStatusAcked, "", false, 10, 5, 300)

	events, err := db.GetPendingEvents(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("expected 0 pending events, got %d", len(events))
	}
}

// TestGetPendingEvents_RespectsBatchLimit verifies batch limit is respected.
func TestGetPendingEvents_RespectsBatchLimit(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	for i := range 5 {
		e := &Event{
			DeliveryID:  string(rune('x' + i)) + "-batch",
			GitHubEvent: "push",
			PayloadJSON: `{}`,
		}
		db.InsertEvent(e)
	}

	events, err := db.GetPendingEvents(3)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("expected batch limit 3, got %d", len(events))
	}
}

// TestGetEventsWithPagination_ZeroOffset verifies default pagination.
func TestGetEventsWithPagination_ZeroOffset(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	e := &Event{DeliveryID: "p0", GitHubEvent: "push", PayloadJSON: `{}`}
	db.InsertEvent(e)

	events, total, err := db.GetEventsWithPagination(10, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("expected total 1, got %d", total)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
}

// TestGetEventsWithPagination_WithOffset verifies offset pagination.
func TestGetEventsWithPagination_WithOffset(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	for i := range 3 {
		e := &Event{
			DeliveryID:  string(rune('p' + i)) + "-offset",
			GitHubEvent: "push",
			PayloadJSON: `{}`,
		}
		db.InsertEvent(e)
	}

	events, total, err := db.GetEventsWithPagination(1, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 {
		t.Fatalf("expected total 3, got %d", total)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event with limit=1 offset=1, got %d", len(events))
	}
}

// TestGetEventsWithPagination_StatusFilter verifies status filter works in pagination.
func TestGetEventsWithPagination_StatusFilter(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	e1 := &Event{DeliveryID: "ps1", GitHubEvent: "push", PayloadJSON: `{}`}
	e2 := &Event{DeliveryID: "ps2", GitHubEvent: "push", PayloadJSON: `{}`}
	db.InsertEvent(e1)
	db.InsertEvent(e2)
	db.UpdateEventStatus(e1.ID, EventStatusAcked, "", false, 10, 5, 300)

	events, total, err := db.GetEventsWithPagination(10, 0, string(EventStatusAcked))
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("expected 1 acked event, got %d", total)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 acked event, got %d", len(events))
	}
}

// TestGetEventsWithPagination_EmptyStatusFilter verifies no result for nonexistent status.
func TestGetEventsWithPagination_EmptyStatusFilter(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	e := &Event{DeliveryID: "ps-none", GitHubEvent: "push", PayloadJSON: `{}`}
	db.InsertEvent(e)

	events, total, err := db.GetEventsWithPagination(10, 0, "dead")
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 {
		t.Fatalf("expected 0 dead events, got %d", total)
	}
	if len(events) != 0 {
		t.Fatalf("expected 0 dead events, got %d", len(events))
	}
}

// TestUpdateAgentLastAck_NonexistentAgent verifies no error for nonexistent agent.
func TestUpdateAgentLastAck_NonexistentAgent(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	err := db.UpdateAgentLastAck("ghost", 10)
	if err != nil {
		t.Fatalf("expected no error for nonexistent agent, got %v", err)
	}
}

// TestDisconnectAgent_Nonexistent verifies no error disconnecting nonexistent agent.
func TestDisconnectAgent_Nonexistent(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	err := db.DisconnectAgent("ghost-agent")
	if err != nil {
		t.Fatalf("expected no error for nonexistent agent, got %v", err)
	}
}

// TestUpsertAgent_Idempotent verifies multiple upserts don't create duplicates.
func TestUpsertAgent_Idempotent(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	db.UpsertAgent("idem-agent")
	db.UpsertAgent("idem-agent")
	db.UpsertAgent("idem-agent")

	agent, err := db.GetAgent("idem-agent")
	if err != nil {
		t.Fatal(err)
	}
	if !agent.Connected {
		t.Fatal("expected agent connected after upserts")
	}
}

// TestEventStatus_Constants verifies status constants are correct strings.
func TestEventStatus_Constants(t *testing.T) {
	if string(EventStatusPending) != "pending" {
		t.Fatalf("expected 'pending', got '%s'", EventStatusPending)
	}
	if string(EventStatusDelivering) != "delivering" {
		t.Fatalf("expected 'delivering', got '%s'", EventStatusDelivering)
	}
	if string(EventStatusAcked) != "acked" {
		t.Fatalf("expected 'acked', got '%s'", EventStatusAcked)
	}
	if string(EventStatusFailed) != "failed" {
		t.Fatalf("expected 'failed', got '%s'", EventStatusFailed)
	}
	if string(EventStatusDead) != "dead" {
		t.Fatalf("expected 'dead', got '%s'", EventStatusDead)
	}
}

// TestUpdateEventStatus_StaleDelivering verifies delivering events without new agent don't block forever.
func TestUpdateEventStatus_StaleDelivering(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	e := &Event{DeliveryID: "stale-del", GitHubEvent: "push", PayloadJSON: `{}`}
	db.InsertEvent(e)

	// First time: set to delivering
	db.UpdateEventStatus(e.ID, EventStatusDelivering, "", false, 10, 5, 300)

	// Second time: set to delivering again (stale delivery)
	err := db.UpdateEventStatus(e.ID, EventStatusFailed, "no agent", true, 10, 5, 300)
	if err != nil {
		t.Fatal(err)
	}

	updated, _ := db.GetEventByID(e.ID)
	if updated.Status != string(EventStatusPending) {
		t.Fatalf("expected pending after stale fix, got %s", updated.Status)
	}
}

// TestGetStatusCounts_AllZero verifies zero counts for empty DB.
func TestGetStatusCounts_AllZero(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	counts, err := db.GetStatusCounts()
	if err != nil {
		t.Fatal(err)
	}
	if counts.Total != 0 {
		t.Fatalf("expected 0 total, got %d", counts.Total)
	}
	if counts.Pending != 0 {
		t.Fatalf("expected 0 pending, got %d", counts.Pending)
	}
}

// TestRetryEvent_Nonexistent verifies retry on nonexistent event doesn't error.
func TestRetryEvent_Nonexistent(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	err := db.RetryEvent(99999)
	if err != nil {
		t.Fatalf("expected no error for nonexistent event retry, got %v", err)
	}
}
