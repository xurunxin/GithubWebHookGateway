package relay

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/nkit/github-webhook-relay/internal/storage"
)

func TestClientManager_Register(t *testing.T) {
	cm := NewClientManager()

	c := cm.Register("agent-01")
	if c == nil {
		t.Fatal("expected client")
	}
	if c.AgentID != "agent-01" {
		t.Fatalf("expected agent-01, got %s", c.AgentID)
	}
	if cm.ConnectedCount() != 1 {
		t.Fatalf("expected 1 connected, got %d", cm.ConnectedCount())
	}
}

func TestClientManager_Unregister(t *testing.T) {
	cm := NewClientManager()

	cm.Register("agent-01")
	cm.Unregister("agent-01")

	if cm.ConnectedCount() != 0 {
		t.Fatalf("expected 0 connected, got %d", cm.ConnectedCount())
	}
}

func TestClientManager_HasConnected(t *testing.T) {
	cm := NewClientManager()

	if cm.HasConnected() {
		t.Fatal("expected no connected clients")
	}

	cm.Register("agent-01")
	if !cm.HasConnected() {
		t.Fatal("expected connected clients")
	}
}

func TestClient_LastAck(t *testing.T) {
	cm := NewClientManager()
	c := cm.Register("agent-01")

	c.SetLastAck(10)
	if c.GetLastAck() != 10 {
		t.Fatalf("expected last ack 10, got %d", c.GetLastAck())
	}

	c.SetLastAck(5)
	if c.GetLastAck() != 10 {
		t.Fatalf("expected last ack still 10 (no downgrade), got %d", c.GetLastAck())
	}

	c.SetLastAck(15)
	if c.GetLastAck() != 15 {
		t.Fatalf("expected last ack 15, got %d", c.GetLastAck())
	}
}

func TestRelay_HandleAck_Ok(t *testing.T) {
	db := setupRelayDB(t)
	defer db.Close()

	e := &storage.Event{
		DeliveryID:  "del-ack-relay",
		GitHubEvent: "push",
		PayloadJSON: `{}`,
	}
	db.InsertEvent(e)

	rly := New(db, RelayConfig{MaxRetry: 10, RetryInitialSecs: 5, RetryMaxSecs: 300, DeliveryBatch: 10})
	cm := rly.ClientManager()
	cm.Register("agent-01")

	rly.HandleAck("agent-01", "ok", e.ID, false, "")

	updated, _ := db.GetEventByID(e.ID)
	if updated.Status != string(storage.EventStatusAcked) {
		t.Fatalf("expected acked, got %s", updated.Status)
	}
}

func TestRelay_HandleAck_Failed_Retryable(t *testing.T) {
	db := setupRelayDB(t)
	defer db.Close()

	e := &storage.Event{
		DeliveryID:  "del-ack-retry",
		GitHubEvent: "issues",
		PayloadJSON: `{}`,
	}
	db.InsertEvent(e)

	rly := New(db, RelayConfig{MaxRetry: 10, RetryInitialSecs: 5, RetryMaxSecs: 300, DeliveryBatch: 10})
	cm := rly.ClientManager()
	cm.Register("agent-01")

	rly.HandleAck("agent-01", "failed", e.ID, true, "timeout")

	updated, _ := db.GetEventByID(e.ID)
	if updated.Status != string(storage.EventStatusPending) {
		t.Fatalf("expected pending for retry, got %s", updated.Status)
	}
	if updated.RetryCount != 1 {
		t.Fatalf("expected retry_count=1, got %d", updated.RetryCount)
	}
}

func TestRelay_HandleAck_Failed_NonRetryable(t *testing.T) {
	db := setupRelayDB(t)
	defer db.Close()

	e := &storage.Event{
		DeliveryID:  "del-ack-dead",
		GitHubEvent: "gollum",
		PayloadJSON: `{}`,
	}
	db.InsertEvent(e)

	rly := New(db, RelayConfig{MaxRetry: 10, RetryInitialSecs: 5, RetryMaxSecs: 300, DeliveryBatch: 10})
	cm := rly.ClientManager()
	cm.Register("agent-01")

	rly.HandleAck("agent-01", "failed", e.ID, false, "unsupported")

	updated, _ := db.GetEventByID(e.ID)
	if updated.Status != string(storage.EventStatusDead) {
		t.Fatalf("expected dead, got %s", updated.Status)
	}
}

func TestJSONMessages(t *testing.T) {
	hello := HelloMessage{Type: "hello", AgentID: "agent-01", LastAckID: 5}
	data, _ := json.Marshal(hello)

	var parsed HelloMessage
	json.Unmarshal(data, &parsed)
	if parsed.Type != "hello" {
		t.Fatalf("expected hello type")
	}
	if parsed.AgentID != "agent-01" {
		t.Fatalf("expected agent-01")
	}

	ack := AckMessage{Type: "ack", EventID: 100, Status: "ok"}
	data2, _ := json.Marshal(ack)

	var parsed2 AckMessage
	json.Unmarshal(data2, &parsed2)
	if parsed2.EventID != 100 {
		t.Fatalf("expected event_id 100")
	}

	ghEvent := GithubEventMessage{
		Type:        "github_event",
		EventID:     42,
		DeliveryID:  "del-42",
		GitHubEvent: "push",
	}
	data3, _ := json.Marshal(ghEvent)

	var parsed3 GithubEventMessage
	json.Unmarshal(data3, &parsed3)
	if parsed3.EventID != 42 {
		t.Fatalf("expected event_id 42")
	}
}

func setupRelayDB(t *testing.T) *storage.DB {
	t.Helper()

	path := t.TempDir() + "/relay_test.db"
	db, err := storage.Open(path)
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
