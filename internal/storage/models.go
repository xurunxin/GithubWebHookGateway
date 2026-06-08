package storage

import "time"

type EventStatus string

const (
	EventStatusPending    EventStatus = "pending"
	EventStatusDelivering EventStatus = "delivering"
	EventStatusAcked      EventStatus = "acked"
	EventStatusFailed     EventStatus = "failed"
	EventStatusDead       EventStatus = "dead"
	EventStatusDuplicate  EventStatus = "duplicate"
)

type Event struct {
	ID                 int64      `json:"id"`
	DeliveryID         string     `json:"delivery_id"`
	GitHubEvent        string     `json:"github_event"`
	RepositoryFullName string     `json:"repository_full_name"`
	InstallationID     int64      `json:"installation_id"`
	PayloadJSON        string     `json:"payload_json,omitempty"`
	Status             string     `json:"status"`
	RetryCount         int        `json:"retry_count"`
	NextRetryAt        *time.Time `json:"next_retry_at,omitempty"`
	LastError          string     `json:"last_error,omitempty"`
	ReceivedAt         time.Time  `json:"received_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	AckedAt            *time.Time `json:"acked_at,omitempty"`
	Payload            any        `json:"payload,omitempty"`
}

type Agent struct {
	ID              int64      `json:"id"`
	AgentID         string     `json:"agent_id"`
	Connected       bool       `json:"connected"`
	LastSeenAt      *time.Time `json:"last_seen_at,omitempty"`
	LastAckEventID  int64      `json:"last_ack_event_id"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type EventLog struct {
	ID        int64     `json:"id"`
	EventID   int64     `json:"event_id"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

type StatusCounts struct {
	Pending    int64 `json:"pending_events"`
	Delivering int64 `json:"delivering_events"`
	Acked      int64 `json:"acked_events"`
	Dead       int64 `json:"dead_events"`
	Failed     int64 `json:"failed_events"`
	Total      int64 `json:"total_events"`
}
