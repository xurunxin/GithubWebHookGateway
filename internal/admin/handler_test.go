package admin

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/nkit/github-webhook-relay/internal/relay"
	"github.com/nkit/github-webhook-relay/internal/storage"
)

func setupAdminDB(t *testing.T) *storage.DB {
	t.Helper()

	path := t.TempDir() + "/admin_test.db"
	db, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}

	migrationPath := findMigrationPath(t)
	if err := db.Migrate(migrationPath); err != nil {
		db.Close()
		t.Fatal(err)
	}

	return db
}

func findMigrationPath(t *testing.T) string {
	t.Helper()

	wd, _ := os.Getwd()
	candidates := []string{
		wd + "/../../migrations/001_init.sql",
		wd + "/migrations/001_init.sql",
		wd + "/../../../migrations/001_init.sql",
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	t.Fatal("cannot find migrations/001_init.sql")
	return ""
}

func TestHealth(t *testing.T) {
	db := setupAdminDB(t)
	defer db.Close()

	rly := relay.New(db, relay.RelayConfig{})
	h := NewHandler("test-admin-token", db, rly)

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	h.Health(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp["ok"] != true {
		t.Fatal("expected ok: true")
	}
	if resp["service"] != "github-webhook-relay" {
		t.Fatalf("expected service name, got %v", resp["service"])
	}
	if _, exists := resp["time"]; !exists {
		t.Fatal("expected time field")
	}
}

func TestStatus_Unauthorized_NoToken(t *testing.T) {
	db := setupAdminDB(t)
	defer db.Close()

	rly := relay.New(db, relay.RelayConfig{})
	h := NewHandler("secret-admin-token", db, rly)

	req := httptest.NewRequest("GET", "/status", nil)
	w := httptest.NewRecorder()
	h.Status(w, req)

	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestStatus_Unauthorized_WrongToken(t *testing.T) {
	db := setupAdminDB(t)
	defer db.Close()

	rly := relay.New(db, relay.RelayConfig{})
	h := NewHandler("secret-admin-token", db, rly)

	req := httptest.NewRequest("GET", "/status", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	w := httptest.NewRecorder()
	h.Status(w, req)

	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestStatus_Authorized(t *testing.T) {
	db := setupAdminDB(t)
	defer db.Close()

	e1 := &storage.Event{DeliveryID: "sa-1", GitHubEvent: "push", PayloadJSON: `{}`}
	e2 := &storage.Event{DeliveryID: "sa-2", GitHubEvent: "pull_request", PayloadJSON: `{}`}
	db.InsertEvent(e1)
	db.InsertEvent(e2)

	rly := relay.New(db, relay.RelayConfig{})
	h := NewHandler("admin-token-123", db, rly)

	req := httptest.NewRequest("GET", "/status", nil)
	req.Header.Set("Authorization", "Bearer admin-token-123")
	w := httptest.NewRecorder()
	h.Status(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp["ok"] != true {
		t.Fatal("expected ok: true")
	}
	if resp["pending_events"].(float64) != 2 {
		t.Fatalf("expected 2 pending, got %v", resp["pending_events"])
	}
}

func TestStatus_QueryTokenAuth(t *testing.T) {
	db := setupAdminDB(t)
	defer db.Close()

	rly := relay.New(db, relay.RelayConfig{})
	h := NewHandler("query-token-admin", db, rly)

	req := httptest.NewRequest("GET", "/status?token=query-token-admin", nil)
	w := httptest.NewRecorder()
	h.Status(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestEvents_Unauthorized(t *testing.T) {
	db := setupAdminDB(t)
	defer db.Close()

	rly := relay.New(db, relay.RelayConfig{})
	h := NewHandler("admin-token", db, rly)

	req := httptest.NewRequest("GET", "/events", nil)
	w := httptest.NewRecorder()
	h.Events(w, req)

	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestEvents_Pagination(t *testing.T) {
	db := setupAdminDB(t)
	defer db.Close()

	for i := range 5 {
		e := &storage.Event{
			DeliveryID:  string(rune('a' + i)) + "-paginate",
			GitHubEvent: "push",
			PayloadJSON: `{}`,
		}
		db.InsertEvent(e)
	}

	rly := relay.New(db, relay.RelayConfig{})
	h := NewHandler("admin-token", db, rly)

	req := httptest.NewRequest("GET", "/events?limit=2&offset=1", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	w := httptest.NewRecorder()
	h.Events(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp["ok"] != true {
		t.Fatal("expected ok: true")
	}

	items, ok := resp["items"].([]any)
	if !ok {
		t.Fatal("expected items array")
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items with limit=2, got %d", len(items))
	}

	total := resp["total"].(float64)
	if total != 5 {
		t.Fatalf("expected total 5, got %v", total)
	}
}

func TestEvents_StatusFilter(t *testing.T) {
	db := setupAdminDB(t)
	defer db.Close()

	e1 := &storage.Event{DeliveryID: "sf-1", GitHubEvent: "push", PayloadJSON: `{}`}
	e2 := &storage.Event{DeliveryID: "sf-2", GitHubEvent: "pull_request", PayloadJSON: `{}`}
	db.InsertEvent(e1)
	db.InsertEvent(e2)
	db.UpdateEventStatus(e1.ID, storage.EventStatusAcked, "", false, 10, 5, 300)

	rly := relay.New(db, relay.RelayConfig{})
	h := NewHandler("admin-token", db, rly)

	req := httptest.NewRequest("GET", "/events?status=acked", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	w := httptest.NewRecorder()
	h.Events(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)

	items := resp["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected 1 acked item, got %d", len(items))
	}
}

func TestEvents_DefaultLimit(t *testing.T) {
	db := setupAdminDB(t)
	defer db.Close()

	for i := range 60 {
		e := &storage.Event{
			DeliveryID:  string(rune('a'+(i/26))) + string(rune('a'+(i%26))) + "-default",
			GitHubEvent: "push",
			PayloadJSON: `{}`,
		}
		db.InsertEvent(e)
	}

	rly := relay.New(db, relay.RelayConfig{})
	h := NewHandler("admin-token", db, rly)

	req := httptest.NewRequest("GET", "/events", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	w := httptest.NewRecorder()
	h.Events(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)

	items := resp["items"].([]any)
	if len(items) != 50 {
		t.Fatalf("expected default limit 50, got %d", len(items))
	}
}

func TestEvents_LimitCap(t *testing.T) {
	db := setupAdminDB(t)
	defer db.Close()

	rly := relay.New(db, relay.RelayConfig{})
	h := NewHandler("admin-token", db, rly)

	req := httptest.NewRequest("GET", "/events?limit=1000", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	w := httptest.NewRecorder()
	h.Events(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)

	limit := resp["limit"].(float64)
	if limit != 500 {
		t.Fatalf("expected limit capped at 500, got %v", limit)
	}
}

func TestRetryEvent_Unauthorized(t *testing.T) {
	db := setupAdminDB(t)
	defer db.Close()

	rly := relay.New(db, relay.RelayConfig{})
	h := NewHandler("admin-token", db, rly)

	req := httptest.NewRequest("POST", "/events/1/retry", nil)
	w := httptest.NewRecorder()
	h.RetryEvent(w, req)

	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestRetryEvent_InvalidID(t *testing.T) {
	db := setupAdminDB(t)
	defer db.Close()

	rly := relay.New(db, relay.RelayConfig{})
	h := NewHandler("admin-token", db, rly)

	req := httptest.NewRequest("POST", "/events/notanumber/retry", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	w := httptest.NewRecorder()
	h.RetryEvent(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestRetryEvent_GetMethod(t *testing.T) {
	db := setupAdminDB(t)
	defer db.Close()

	rly := relay.New(db, relay.RelayConfig{})
	h := NewHandler("admin-token", db, rly)

	req := httptest.NewRequest("GET", "/events/1/retry", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	w := httptest.NewRecorder()
	h.RetryEvent(w, req)

	if w.Code != 405 {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestRetryEvent_Success(t *testing.T) {
	db := setupAdminDB(t)
	defer db.Close()

	e := &storage.Event{
		DeliveryID:  "retry-test-01",
		GitHubEvent: "push",
		PayloadJSON: `{}`,
	}
	db.InsertEvent(e)
	db.UpdateEventStatus(e.ID, storage.EventStatusFailed, "some error", false, 10, 5, 300)

	rly := relay.New(db, relay.RelayConfig{})
	h := NewHandler("admin-token", db, rly)

	req := httptest.NewRequest("POST", "/events/1/retry", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	h.RetryEvent(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp["ok"] != true {
		t.Fatal("expected ok: true")
	}
	if resp["message"] != "retried" {
		t.Fatalf("expected 'retried', got %v", resp["message"])
	}

	updated, _ := db.GetEventByID(e.ID)
	if updated.Status != string(storage.EventStatusPending) {
		t.Fatalf("expected pending after retry, got %s", updated.Status)
	}
}

func TestRequireAuth_NoAuth(t *testing.T) {
	h := NewHandler("my-token", nil, nil)

	req := httptest.NewRequest("GET", "/status", nil)
	if h.requireAuth(req) {
		t.Fatal("expected false for no auth")
	}
}

func TestRequireAuth_BearerHeader(t *testing.T) {
	h := NewHandler("my-token", nil, nil)

	req := httptest.NewRequest("GET", "/status", nil)
	req.Header.Set("Authorization", "Bearer my-token")
	if !h.requireAuth(req) {
		t.Fatal("expected true for correct Bearer token")
	}
}

func TestRequireAuth_QueryToken(t *testing.T) {
	h := NewHandler("my-query-token", nil, nil)

	req := httptest.NewRequest("GET", "/status?token=my-query-token", nil)
	if !h.requireAuth(req) {
		t.Fatal("expected true for correct query token")
	}
}

func TestRequireAuth_WrongToken(t *testing.T) {
	h := NewHandler("my-token", nil, nil)

	req := httptest.NewRequest("GET", "/status", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	if h.requireAuth(req) {
		t.Fatal("expected false for wrong token")
	}
}

func TestRequireAuth_HeaderPriority(t *testing.T) {
	h := NewHandler("header-token", nil, nil)

	req := httptest.NewRequest("GET", "/status?token=query-token", nil)
	req.Header.Set("Authorization", "Bearer header-token")
	if !h.requireAuth(req) {
		t.Fatal("expected true - Bearer header should take priority")
	}
}
