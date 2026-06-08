package admin

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nkit/github-webhook-relay/internal/relay"
	"github.com/nkit/github-webhook-relay/internal/storage"
)

type Handler struct {
	adminToken string
	db         *storage.DB
	relay      *relay.Relay
}

func NewHandler(adminToken string, db *storage.DB, r *relay.Relay) *Handler {
	return &Handler{
		adminToken: adminToken,
		db:         db,
		relay:      r,
	}
}

func (h *Handler) requireAuth(r *http.Request) bool {
	token := ""
	if auth := r.Header.Get("Authorization"); auth != "" {
		const prefix = "Bearer "
		if strings.HasPrefix(auth, prefix) {
			token = auth[len(prefix):]
		}
	}
	if token == "" {
		if q := r.URL.Query().Get("token"); q != "" {
			token = q
		}
	}
	return token == h.adminToken && token != ""
}

func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"service": "github-webhook-relay",
		"time":    time.Now().UTC().Format(time.RFC3339),
	})
}

func (h *Handler) Status(w http.ResponseWriter, r *http.Request) {
	if !h.requireAuth(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "unauthorized"})
		return
	}

	counts, err := h.db.GetStatusCounts()
	if err != nil {
		log.Printf("[admin] get status error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "database_error"})
		return
	}

	connectedCount := h.relay.ClientManager().ConnectedCount()

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                true,
		"agent_connected":   connectedCount > 0,
		"connected_agents":  connectedCount,
		"pending_events":    counts.Pending,
		"delivering_events": counts.Delivering,
		"acked_events":      counts.Acked,
		"dead_events":       counts.Dead,
		"failed_events":     counts.Failed,
		"total_events":      counts.Total,
	})
}

func (h *Handler) Events(w http.ResponseWriter, r *http.Request) {
	if !h.requireAuth(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "unauthorized"})
		return
	}

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			if v > 500 {
				v = 500
			}
			limit = v
		}
	}

	offset := 0
	if o := r.URL.Query().Get("offset"); o != "" {
		if v, err := strconv.Atoi(o); err == nil && v >= 0 {
			offset = v
		}
	}

	statusFilter := r.URL.Query().Get("status")

	events, total, err := h.db.GetEventsWithPagination(limit, offset, statusFilter)
	if err != nil {
		log.Printf("[admin] get events error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "database_error"})
		return
	}

	type item struct {
		ID                 int64      `json:"id"`
		DeliveryID         string     `json:"delivery_id"`
		GitHubEvent        string     `json:"github_event"`
		RepositoryFullName string     `json:"repository_full_name"`
		Status             string     `json:"status"`
		RetryCount         int        `json:"retry_count"`
		NextRetryAt        *time.Time `json:"next_retry_at,omitempty"`
		LastError          string     `json:"last_error,omitempty"`
		ReceivedAt         time.Time  `json:"received_at"`
		UpdatedAt          time.Time  `json:"updated_at"`
		AckedAt            *time.Time `json:"acked_at,omitempty"`
	}

	items := make([]item, 0, len(events))
	for _, e := range events {
		items = append(items, item{
			ID:                 e.ID,
			DeliveryID:         e.DeliveryID,
			GitHubEvent:        e.GitHubEvent,
			RepositoryFullName: e.RepositoryFullName,
			Status:             e.Status,
			RetryCount:         e.RetryCount,
			NextRetryAt:        e.NextRetryAt,
			LastError:          e.LastError,
			ReceivedAt:         e.ReceivedAt,
			UpdatedAt:          e.UpdatedAt,
			AckedAt:            e.AckedAt,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"items":  items,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

func (h *Handler) RetryEvent(w http.ResponseWriter, r *http.Request) {
	if !h.requireAuth(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "unauthorized"})
		return
	}

	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method_not_allowed"})
		return
	}

	idStr := r.PathValue("id")
	if idStr == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "missing_event_id"})
		return
	}

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid_event_id"})
		return
	}

	if err := h.db.RetryEvent(id); err != nil {
		log.Printf("[admin] retry event %d error: %v", id, err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "database_error"})
		return
	}

	log.Printf("[admin] event %d retried", id)
	h.relay.NotifyNewEvent()

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"message":  "retried",
		"event_id": id,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("[admin] write response error: %v", err)
	}
}
