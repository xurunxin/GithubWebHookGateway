package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/nkit/github-webhook-relay/internal/relay"
	"github.com/nkit/github-webhook-relay/internal/storage"
)

type Handler struct {
	secret string
	db     *storage.DB
	relay  *relay.Relay
}

func NewHandler(secret string, db *storage.DB, r *relay.Relay) *Handler {
	return &Handler{
		secret: secret,
		db:     db,
		relay:  r,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, errorResponse{OK: false, Error: "method_not_allowed"})
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 25*1024*1024))
	if err != nil {
		log.Printf("[webhook] error reading body: %v", err)
		writeJSON(w, http.StatusBadRequest, errorResponse{OK: false, Error: "read_body_failed"})
		return
	}

	signature := r.Header.Get("X-Hub-Signature-256")
	eventType := r.Header.Get("X-GitHub-Event")
	deliveryID := r.Header.Get("X-GitHub-Delivery")

	if deliveryID == "" {
		log.Printf("[webhook] missing X-GitHub-Delivery header")
		writeJSON(w, http.StatusBadRequest, errorResponse{OK: false, Error: "missing_delivery_id"})
		return
	}

	if eventType == "" {
		log.Printf("[webhook] missing X-GitHub-Event header")
		writeJSON(w, http.StatusBadRequest, errorResponse{OK: false, Error: "missing_event_type"})
		return
	}

	if !verifySignature(h.secret, body, signature) {
		log.Printf("[webhook] invalid signature delivery=%s event=%s", deliveryID, eventType)
		writeJSON(w, http.StatusUnauthorized, errorResponse{OK: false, Error: "invalid_signature"})
		return
	}

	existing, err := h.db.GetEventByDeliveryID(deliveryID)
	if err != nil {
		log.Printf("[webhook] db error checking duplicate delivery=%s: %v", deliveryID, err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{OK: false, Error: "database_error"})
		return
	}
	if existing != nil {
		log.Printf("[webhook] duplicate delivery=%s event=%s (existing id=%d)", deliveryID, eventType, existing.ID)
		writeJSON(w, http.StatusOK, acceptResponse{
			OK:         true,
			Message:    "duplicate",
			DeliveryID: deliveryID,
		})
		return
	}

	var bodyJSON any
	if err := json.Unmarshal(body, &bodyJSON); err != nil {
		log.Printf("[webhook] invalid JSON delivery=%s: %v", deliveryID, err)
		writeJSON(w, http.StatusBadRequest, errorResponse{OK: false, Error: "invalid_json"})
		return
	}

	bodyMap, ok := bodyJSON.(map[string]any)
	if !ok {
		log.Printf("[webhook] payload not an object delivery=%s", deliveryID)
		writeJSON(w, http.StatusBadRequest, errorResponse{OK: false, Error: "invalid_payload"})
		return
	}

	repoFullName := extractRepoFullName(bodyMap)
	installationID := extractInstallationID(bodyMap)

	event := &storage.Event{
		DeliveryID:         deliveryID,
		GitHubEvent:        eventType,
		RepositoryFullName: repoFullName,
		InstallationID:     installationID,
		PayloadJSON:        string(body),
		Status:             "pending",
	}

	if err := h.db.InsertEvent(event); err != nil {
		log.Printf("[webhook] insert event failed delivery=%s: %v", deliveryID, err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{OK: false, Error: "database_error"})
		return
	}

	log.Printf("[webhook] received delivery=%s event=%s repo=%s id=%d", deliveryID, eventType, repoFullName, event.ID)

	_ = h.db.InsertEventLog(event.ID, "info", fmt.Sprintf("webhook received: %s", eventType))

	h.relay.NotifyNewEvent()

	writeJSON(w, http.StatusAccepted, acceptResponse{
		OK:         true,
		Message:    "accepted",
		DeliveryID: deliveryID,
	})
}

func verifySignature(secret string, body []byte, signatureHeader string) bool {
	if secret == "" || signatureHeader == "" {
		return false
	}

	const prefix = "sha256="
	if !strings.HasPrefix(signatureHeader, prefix) {
		return false
	}
	expectedHex := signatureHeader[len(prefix):]

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	actualHex := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(actualHex), []byte(expectedHex))
}

func extractRepoFullName(payload map[string]any) string {
	if repo, ok := payload["repository"].(map[string]any); ok {
		if name, ok := repo["full_name"].(string); ok {
			return name
		}
	}
	return ""
}

func extractInstallationID(payload map[string]any) int64 {
	if inst, ok := payload["installation"].(map[string]any); ok {
		if id, ok := inst["id"].(float64); ok {
			return int64(id)
		}
	}
	return 0
}

type acceptResponse struct {
	OK         bool   `json:"ok"`
	Message    string `json:"message"`
	DeliveryID string `json:"delivery_id"`
}

type errorResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("[webhook] write response error: %v", err)
	}
}
