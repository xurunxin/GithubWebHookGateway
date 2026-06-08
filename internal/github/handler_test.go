package github

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/nkit/github-webhook-relay/internal/config"
	"github.com/nkit/github-webhook-relay/internal/relay"
	"github.com/nkit/github-webhook-relay/internal/storage"
)

func TestVerifySignature_Valid(t *testing.T) {
	secret := "test-secret-123"
	body := []byte(`{"action":"opened","pull_request":{"id":1}}`)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if !verifySignature(secret, body, sig) {
		t.Fatal("expected valid signature")
	}
}

func TestVerifySignature_Invalid(t *testing.T) {
	secret := "test-secret-123"
	body := []byte(`{"action":"opened"}`)

	if verifySignature(secret, body, "sha256=deadbeef") {
		t.Fatal("expected invalid signature")
	}
}

func TestVerifySignature_MissingPrefix(t *testing.T) {
	secret := "test-secret-123"
	body := []byte(`{"action":"opened"}`)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := hex.EncodeToString(mac.Sum(nil))

	if verifySignature(secret, body, sig) {
		t.Fatal("expected invalid signature without prefix")
	}
}

func TestVerifySignature_EmptySecret(t *testing.T) {
	body := []byte(`{"action":"opened"}`)

	if verifySignature("", body, "sha256=abc") {
		t.Fatal("expected invalid signature with empty secret")
	}
}

func TestExtractRepoFullName(t *testing.T) {
	payload := map[string]any{
		"repository": map[string]any{
			"full_name": "owner/repo",
		},
	}

	name := extractRepoFullName(payload)
	if name != "owner/repo" {
		t.Fatalf("expected 'owner/repo', got '%s'", name)
	}
}

func TestExtractRepoFullName_Missing(t *testing.T) {
	payload := map[string]any{}

	name := extractRepoFullName(payload)
	if name != "" {
		t.Fatalf("expected empty, got '%s'", name)
	}
}

func TestExtractInstallationID(t *testing.T) {
	payload := map[string]any{
		"installation": map[string]any{
			"id": float64(12345),
		},
	}

	id := extractInstallationID(payload)
	if id != 12345 {
		t.Fatalf("expected 12345, got %d", id)
	}
}

func TestExtractInstallationID_Missing(t *testing.T) {
	payload := map[string]any{}

	id := extractInstallationID(payload)
	if id != 0 {
		t.Fatalf("expected 0, got %d", id)
	}
}

func TestHandler_Webhook_DuplicateDelivery(t *testing.T) {
	secret := "test-secret"
	body := []byte(`{"action":"opened","repository":{"full_name":"test/repo"}}`)
	deliveryID := "test-delivery-001"

	dbPath := t.TempDir() + "/test.db"
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	migrationPath := findMigrationPath(t)
	if err := db.Migrate(migrationPath); err != nil {
		t.Fatal(err)
	}

	relayCfg := relay.RelayConfig{
		MaxRetry:         10,
		RetryInitialSecs: 5,
		RetryMaxSecs:     300,
		DeliveryBatch:    10,
	}
	rly := relay.New(db, relayCfg)

	handler := NewHandler(secret, db, rly)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest("POST", "/webhook/github", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", "pull_request")
	req.Header.Set("X-GitHub-Delivery", deliveryID)
	req.Header.Set("X-Hub-Signature-256", sig)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 202 {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}

	var resp1 acceptResponse
	json.Unmarshal(w.Body.Bytes(), &resp1)
	if resp1.Message != "accepted" {
		t.Fatalf("expected 'accepted', got '%s'", resp1.Message)
	}

	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("POST", "/webhook/github", bytes.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-GitHub-Event", "pull_request")
	req2.Header.Set("X-GitHub-Delivery", deliveryID)
	req2.Header.Set("X-Hub-Signature-256", sig)
	handler.ServeHTTP(w2, req2)

	if w2.Code != 200 {
		t.Fatalf("expected 200 for duplicate, got %d: %s", w2.Code, w2.Body.String())
	}

	var resp2 acceptResponse
	json.Unmarshal(w2.Body.Bytes(), &resp2)
	if resp2.Message != "duplicate" {
		t.Fatalf("expected 'duplicate', got '%s'", resp2.Message)
	}

	events, _, _ := db.GetEventsWithPagination(10, 0, "")
	if len(events) != 1 {
		t.Fatalf("expected 1 event in DB, got %d", len(events))
	}
}

func TestHandler_Webhook_InvalidSignature(t *testing.T) {
	secret := "test-secret"
	body := []byte(`{"action":"opened"}`)

	dbPath := t.TempDir() + "/test.db"
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	migrationPath := findMigrationPath(t)
	if err := db.Migrate(migrationPath); err != nil {
		t.Fatal(err)
	}

	relayCfg := relay.RelayConfig{MaxRetry: 10, RetryInitialSecs: 5, RetryMaxSecs: 300, DeliveryBatch: 10}
	rly := relay.New(db, relayCfg)

	handler := NewHandler(secret, db, rly)

	req := httptest.NewRequest("POST", "/webhook/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "pull_request")
	req.Header.Set("X-GitHub-Delivery", "test-delivery-002")
	req.Header.Set("X-Hub-Signature-256", "sha256=invalid")

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestHandler_Webhook_MissingSignature(t *testing.T) {
	secret := "test-secret"
	body := []byte(`{"action":"opened"}`)

	dbPath := t.TempDir() + "/test.db"
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	migrationPath := findMigrationPath(t)
	if err := db.Migrate(migrationPath); err != nil {
		t.Fatal(err)
	}

	relayCfg := relay.RelayConfig{MaxRetry: 10, RetryInitialSecs: 5, RetryMaxSecs: 300, DeliveryBatch: 10}
	rly := relay.New(db, relayCfg)

	handler := NewHandler(secret, db, rly)

	req := httptest.NewRequest("POST", "/webhook/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "pull_request")
	req.Header.Set("X-GitHub-Delivery", "test-delivery-003")

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestHandler_Webhook_MissingDeliveryID(t *testing.T) {
	secret := "test-secret"
	body := []byte(`{"action":"opened"}`)

	dbPath := t.TempDir() + "/test.db"
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	migrationPath := findMigrationPath(t)
	if err := db.Migrate(migrationPath); err != nil {
		t.Fatal(err)
	}

	relayCfg := relay.RelayConfig{MaxRetry: 10, RetryInitialSecs: 5, RetryMaxSecs: 300, DeliveryBatch: 10}
	rly := relay.New(db, relayCfg)

	handler := NewHandler(secret, db, rly)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest("POST", "/webhook/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "pull_request")
	req.Header.Set("X-Hub-Signature-256", sig)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func findMigrationPath(t *testing.T) string {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	candidates := []string{
		wd + "/migrations/001_init.sql",
		wd + "/../../migrations/001_init.sql",
		wd + "/../../../migrations/001_init.sql",
	}

	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	if strings.HasSuffix(wd, "internal/github") {
		return wd + "/../../migrations/001_init.sql"
	}

	t.Fatal("cannot find migrations/001_init.sql")
	return ""
}

func TestConfig_Defaults(t *testing.T) {
	os.Unsetenv("APP_ENV")
	os.Unsetenv("HTTP_ADDR")
	os.Unsetenv("GITHUB_WEBHOOK_SECRET")

	cfg := config.Load()

	if cfg.HTTPAddr != "0.0.0.0:8080" {
		t.Fatalf("expected default HTTPAddr, got %s", cfg.HTTPAddr)
	}
	if cfg.EventMaxRetry != 10 {
		t.Fatalf("expected default max retry 10, got %d", cfg.EventMaxRetry)
	}
	if cfg.EventDeliveryBatchSize != 10 {
		t.Fatalf("expected default batch 10, got %d", cfg.EventDeliveryBatchSize)
	}
}

func TestConfig_EnvOverride(t *testing.T) {
	os.Setenv("EVENT_MAX_RETRY", "5")
	defer os.Unsetenv("EVENT_MAX_RETRY")

	cfg := config.Load()
	if cfg.EventMaxRetry != 5 {
		t.Fatalf("expected max retry 5 from env, got %d", cfg.EventMaxRetry)
	}
}
