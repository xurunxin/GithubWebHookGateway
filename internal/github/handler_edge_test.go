package github

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/nkit/github-webhook-relay/internal/relay"
	"github.com/nkit/github-webhook-relay/internal/storage"
)

// TestHandler_Webhook_InvalidJSON verifies 400 for non-JSON body.
func TestHandler_Webhook_InvalidJSON(t *testing.T) {
	secret := "test-secret"
	body := []byte(`not json`)

	dbPath := t.TempDir() + "/edge_test.db"
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := db.Migrate(findMigrationPath(t)); err != nil {
		t.Fatal(err)
	}

	rly := relay.New(db, relay.RelayConfig{MaxRetry: 10, RetryInitialSecs: 5, RetryMaxSecs: 300, DeliveryBatch: 10})
	handler := NewHandler(secret, db, rly)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest("POST", "/webhook/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-GitHub-Delivery", "invalid-json-001")
	req.Header.Set("X-Hub-Signature-256", sig)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400 for invalid JSON, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandler_Webhook_GetMethod verifies 405 for GET requests.
func TestHandler_Webhook_GetMethod(t *testing.T) {
	secret := "test-secret"

	dbPath := t.TempDir() + "/edge_test.db"
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := db.Migrate(findMigrationPath(t)); err != nil {
		t.Fatal(err)
	}

	rly := relay.New(db, relay.RelayConfig{MaxRetry: 10, RetryInitialSecs: 5, RetryMaxSecs: 300, DeliveryBatch: 10})
	handler := NewHandler(secret, db, rly)

	req := httptest.NewRequest("GET", "/webhook/github", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 405 {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

// TestHandler_Webhook_MissingEventType verifies 400 for missing X-GitHub-Event.
func TestHandler_Webhook_MissingEventType(t *testing.T) {
	secret := "test-secret"
	body := []byte(`{"action":"opened"}`)

	dbPath := t.TempDir() + "/edge_test.db"
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := db.Migrate(findMigrationPath(t)); err != nil {
		t.Fatal(err)
	}

	rly := relay.New(db, relay.RelayConfig{MaxRetry: 10, RetryInitialSecs: 5, RetryMaxSecs: 300, DeliveryBatch: 10})
	handler := NewHandler(secret, db, rly)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest("POST", "/webhook/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Delivery", "no-event-type")
	req.Header.Set("X-Hub-Signature-256", sig)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandler_Webhook_ScopedPayload verifies extraction from a realistic GitHub payload.
func TestHandler_Webhook_ScopedPayload(t *testing.T) {
	secret := "test-secret"
	body := []byte(`{
		"action": "opened",
		"number": 1,
		"pull_request": {"id": 1, "title": "test"},
		"repository": {"full_name": "myorg/myrepo", "id": 100},
		"installation": {"id": 999}
	}`)

	dbPath := t.TempDir() + "/edge_test.db"
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := db.Migrate(findMigrationPath(t)); err != nil {
		t.Fatal(err)
	}

	rly := relay.New(db, relay.RelayConfig{MaxRetry: 10, RetryInitialSecs: 5, RetryMaxSecs: 300, DeliveryBatch: 10})
	handler := NewHandler(secret, db, rly)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest("POST", "/webhook/github", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", "pull_request")
	req.Header.Set("X-GitHub-Delivery", "scoped-001")
	req.Header.Set("X-Hub-Signature-256", sig)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 202 {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}

	event, _ := db.GetEventByDeliveryID("scoped-001")
	if event.RepositoryFullName != "myorg/myrepo" {
		t.Fatalf("expected 'myorg/myrepo', got '%s'", event.RepositoryFullName)
	}
	if event.InstallationID != 999 {
		t.Fatalf("expected installation_id 999, got %d", event.InstallationID)
	}
}

// TestHandler_Response_JSONFormat verifies all response JSON fields are correct.
func TestHandler_Response_JSONFormat(t *testing.T) {
	secret := "test-secret"
	body := []byte(`{"action":"opened","repository":{"full_name":"test/repo"}}`)

	dbPath := t.TempDir() + "/edge_test.db"
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := db.Migrate(findMigrationPath(t)); err != nil {
		t.Fatal(err)
	}

	rly := relay.New(db, relay.RelayConfig{MaxRetry: 10, RetryInitialSecs: 5, RetryMaxSecs: 300, DeliveryBatch: 10})
	handler := NewHandler(secret, db, rly)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest("POST", "/webhook/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-GitHub-Delivery", "response-format")
	req.Header.Set("X-Hub-Signature-256", sig)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 202 {
		t.Fatalf("expected 202, got %d", w.Code)
	}

	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Fatalf("expected Content-Type: application/json, got '%s'", contentType)
	}

	var resp acceptResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response should be valid JSON: %v", err)
	}
	if !resp.OK {
		t.Fatal("expected ok: true")
	}
	if resp.Message != "accepted" {
		t.Fatalf("expected 'accepted', got '%s'", resp.Message)
	}
	if resp.DeliveryID != "response-format" {
		t.Fatalf("expected delivery_id 'response-format', got '%s'", resp.DeliveryID)
	}
}

// TestVerifySignature_TamperedBody verifies signature fails with altered body.
func TestVerifySignature_TamperedBody(t *testing.T) {
	secret := "test-secret"
	body := []byte(`{"action":"opened"}`)
	tampered := []byte(`{"action":"closed"}`)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if verifySignature(secret, tampered, sig) {
		t.Fatal("expected invalid signature for tampered body")
	}
}

// TestVerifySignature_DifferentSecret verifies signature fails with wrong secret.
func TestVerifySignature_DifferentSecret(t *testing.T) {
	body := []byte(`{"action":"opened"}`)

	mac := hmac.New(sha256.New, []byte("secret-A"))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if verifySignature("secret-B", body, sig) {
		t.Fatal("expected invalid signature with different secret")
	}
}

// TestVerifySignature_EmptyBody verifies signature works with empty body.
func TestVerifySignature_EmptyBody(t *testing.T) {
	secret := "test-secret"
	body := []byte{}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if !verifySignature(secret, body, sig) {
		t.Fatal("expected valid signature for empty body")
	}
}

// TestExtractRepoFullName_NestedRepository verifies extraction when repository is nested.
func TestExtractRepoFullName_NestedRepository(t *testing.T) {
	payload := map[string]any{
		"repository": map[string]any{
			"full_name": "org/nested-repo",
			"id":        123.0,
		},
	}
	name := extractRepoFullName(payload)
	if name != "org/nested-repo" {
		t.Fatalf("expected 'org/nested-repo', got '%s'", name)
	}
}

// TestExtractRepoFullName_NotAMap verifies extraction when repository is not a map.
func TestExtractRepoFullName_NotAMap(t *testing.T) {
	payload := map[string]any{
		"repository": "not-a-map",
	}
	name := extractRepoFullName(payload)
	if name != "" {
		t.Fatalf("expected empty, got '%s'", name)
	}
}

// TestExtractInstallationID_NotANumber verifies extraction when installation ID is not numeric.
func TestExtractInstallationID_NotANumber(t *testing.T) {
	payload := map[string]any{
		"installation": map[string]any{
			"id": "not-a-number",
		},
	}
	id := extractInstallationID(payload)
	if id != 0 {
		t.Fatalf("expected 0 for non-numeric ID, got %d", id)
	}
}

// TestExtractInstallationID_NotAMap verifies extraction when installation is not a map.
func TestExtractInstallationID_NotAMap(t *testing.T) {
	payload := map[string]any{
		"installation": "not-a-map",
	}
	id := extractInstallationID(payload)
	if id != 0 {
		t.Fatalf("expected 0 for non-map installation, got %d", id)
	}
}

// TestHandler_Webhook_ArrayPayload verifies 400 for JSON arrays (not objects).
func TestHandler_Webhook_ArrayPayload(t *testing.T) {
	secret := "test-secret"
	body := []byte(`[{"action":"opened"}]`)

	dbPath := t.TempDir() + "/edge_test.db"
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := db.Migrate(findMigrationPath(t)); err != nil {
		t.Fatal(err)
	}

	rly := relay.New(db, relay.RelayConfig{MaxRetry: 10, RetryInitialSecs: 5, RetryMaxSecs: 300, DeliveryBatch: 10})
	handler := NewHandler(secret, db, rly)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest("POST", "/webhook/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-GitHub-Delivery", "array-payload")
	req.Header.Set("X-Hub-Signature-256", sig)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400 for array payload, got %d", w.Code)
	}
}
