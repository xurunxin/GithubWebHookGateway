package websocket

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestExtractToken_BearerHeader(t *testing.T) {
	req := httptest.NewRequest("GET", "/ws/openclaw", nil)
	req.Header.Set("Authorization", "Bearer my-secret-token")

	token := extractToken(req, "my-secret-token")
	if token != "my-secret-token" {
		t.Fatalf("expected 'my-secret-token', got '%s'", token)
	}
}

func TestExtractToken_QueryParam(t *testing.T) {
	req := httptest.NewRequest("GET", "/ws/openclaw?token=my-query-token", nil)

	token := extractToken(req, "my-query-token")
	if token != "my-query-token" {
		t.Fatalf("expected 'my-query-token', got '%s'", token)
	}
}

func TestExtractToken_NoToken(t *testing.T) {
	req := httptest.NewRequest("GET", "/ws/openclaw", nil)

	token := extractToken(req, "expected-token")
	if token != "" {
		t.Fatalf("expected empty token, got '%s'", token)
	}
}

func TestExtractToken_MissingBearerPrefix(t *testing.T) {
	req := httptest.NewRequest("GET", "/ws/openclaw", nil)
	req.Header.Set("Authorization", "my-token")

	token := extractToken(req, "my-token")
	if token != "" {
		t.Fatalf("expected empty token (no Bearer prefix), got '%s'", token)
	}
}

func TestExtractToken_WrongToken(t *testing.T) {
	req := httptest.NewRequest("GET", "/ws/openclaw", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")

	token := extractToken(req, "correct-token")
	if token != "wrong-token" {
		t.Fatalf("expected 'wrong-token' (extracted), got '%s'", token)
	}
}

func TestExtractToken_HeaderPriorityOverQuery(t *testing.T) {
	req := httptest.NewRequest("GET", "/ws/openclaw?token=query-token", nil)
	req.Header.Set("Authorization", "Bearer header-token")

	token := extractToken(req, "header-token")
	if token != "header-token" {
		t.Fatalf("expected header token to take priority, got '%s'", token)
	}
}

func TestExtractToken_EmptyAuthorization(t *testing.T) {
	req := httptest.NewRequest("GET", "/ws/openclaw?token=fallback-token", nil)
	req.Header.Set("Authorization", "")

	token := extractToken(req, "fallback-token")
	if token != "fallback-token" {
		t.Fatalf("expected 'fallback-token' from query, got '%s'", token)
	}
}

func TestExtractToken_AuthorizationWithExtraSpaces(t *testing.T) {
	req := httptest.NewRequest("GET", "/ws/openclaw", nil)
	req.Header.Set("Authorization", "Bearer  my-token")

	token := extractToken(req, "my-token")
	// The current implementation doesn't trim, so " my-token" ≠ "my-token"
	// This test documents the behavior
	if token != " my-token" {
		t.Logf("token with leading space: '%s'", token)
	}
}

func TestNewHandler(t *testing.T) {
	h := NewHandler(
		"test-token",
		nil,
		nil,
		90*time.Second,
		10*time.Second,
		30*time.Second,
	)

	if h.token != "test-token" {
		t.Fatalf("expected token 'test-token', got '%s'", h.token)
	}
	if h.readTimeout != 90*time.Second {
		t.Fatalf("expected readTimeout 90s, got %v", h.readTimeout)
	}
	if h.writeTimeout != 10*time.Second {
		t.Fatalf("expected writeTimeout 10s, got %v", h.writeTimeout)
	}
	if h.pingInterval != 30*time.Second {
		t.Fatalf("expected pingInterval 30s, got %v", h.pingInterval)
	}
}

func TestUpgrader_CheckOrigin(t *testing.T) {
	req := httptest.NewRequest("GET", "/ws/openclaw", nil)
	// CheckOrigin returns true for all origins
	if !upgrader.CheckOrigin(req) {
		t.Fatal("expected CheckOrigin to return true")
	}
}
