package relay

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/nkit/github-webhook-relay/internal/storage"
)

type Client struct {
	AgentID  string
	SendCh   chan []byte
	LastAck  int64
	mu       sync.RWMutex
}

type ClientManager struct {
	mu      sync.RWMutex
	clients map[string]*Client
}

func NewClientManager() *ClientManager {
	return &ClientManager{
		clients: make(map[string]*Client),
	}
}

func (cm *ClientManager) Register(agentID string) *Client {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	c, exists := cm.clients[agentID]
	if exists {
		c.mu.Lock()
		close(c.SendCh)
		c.mu.Unlock()
	}

	c = &Client{
		AgentID: agentID,
		SendCh:  make(chan []byte, 64),
	}
	cm.clients[agentID] = c
	return c
}

func (cm *ClientManager) Unregister(agentID string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if c, ok := cm.clients[agentID]; ok {
		c.mu.Lock()
		select {
		case <-c.SendCh:
		default:
			close(c.SendCh)
		}
		c.mu.Unlock()
		delete(cm.clients, agentID)
	}
}

func (cm *ClientManager) GetClient(agentID string) *Client {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.clients[agentID]
}

func (cm *ClientManager) ConnectedCount() int {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return len(cm.clients)
}

func (cm *ClientManager) HasConnected() bool {
	return cm.ConnectedCount() > 0
}

func (c *Client) SetLastAck(id int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if id > c.LastAck {
		c.LastAck = id
	}
}

func (c *Client) GetLastAck() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.LastAck
}

type Relay struct {
	db       *storage.DB
	cfg      RelayConfig
	cm       *ClientManager
	notifyCh chan struct{}
	done     chan struct{}
	wg       sync.WaitGroup
}

type RelayConfig struct {
	MaxRetry         int
	RetryInitialSecs int
	RetryMaxSecs     int
	DeliveryBatch    int
}

func New(db *storage.DB, cfg RelayConfig) *Relay {
	return &Relay{
		db:       db,
		cfg:      cfg,
		cm:       NewClientManager(),
		notifyCh: make(chan struct{}, 100),
		done:     make(chan struct{}),
	}
}

func (r *Relay) ClientManager() *ClientManager {
	return r.cm
}

func (r *Relay) Config() RelayConfig {
	return r.cfg
}

func (r *Relay) Start() {
	r.wg.Add(1)
	go r.dispatchLoop()
	log.Printf("[relay] started (batch=%d, max_retry=%d)", r.cfg.DeliveryBatch, r.cfg.MaxRetry)
}

func (r *Relay) Stop() {
	close(r.done)
	r.wg.Wait()
	log.Printf("[relay] stopped")
}

func (r *Relay) NotifyNewEvent() {
	select {
	case r.notifyCh <- struct{}{}:
	default:
	}
}

func (r *Relay) dispatchLoop() {
	defer r.wg.Done()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.done:
			return
		case <-r.notifyCh:
			r.dispatchPending()
		case <-ticker.C:
			r.dispatchPending()
		}
	}
}

func (r *Relay) dispatchPending() {
	if !r.cm.HasConnected() {
		return
	}

	for {
		events, err := r.db.GetPendingEvents(r.cfg.DeliveryBatch)
		if err != nil {
			log.Printf("[relay] get pending events error: %v", err)
			return
		}
		if len(events) == 0 {
			return
		}

		r.deliverEvents(events)
	}
}

func (r *Relay) deliverEvents(events []*storage.Event) {
	r.cm.mu.RLock()
	clients := make([]*Client, 0, len(r.cm.clients))
	for _, c := range r.cm.clients {
		clients = append(clients, c)
	}
	r.cm.mu.RUnlock()

	for _, e := range events {
		_ = r.db.UpdateEventStatus(e.ID, storage.EventStatusDelivering, "", false,
			r.cfg.MaxRetry, r.cfg.RetryInitialSecs, r.cfg.RetryMaxSecs)

		msg := GithubEventMessage{
			Type:               "github_event",
			EventID:            e.ID,
			DeliveryID:         e.DeliveryID,
			GitHubEvent:        e.GitHubEvent,
			RepositoryFullName: e.RepositoryFullName,
			InstallationID:     e.InstallationID,
			ReceivedAt:         e.ReceivedAt.Format(time.RFC3339),
			Payload:            json.RawMessage(e.PayloadJSON),
		}

		data, err := json.Marshal(msg)
		if err != nil {
			log.Printf("[relay] marshal event %d error: %v", e.ID, err)
			continue
		}

		for _, c := range clients {
			select {
			case c.SendCh <- data:
				log.Printf("[relay] event %d dispatched to agent %s", e.ID, c.AgentID)
			default:
				log.Printf("[relay] agent %s send buffer full, dropping event %d", c.AgentID, e.ID)
				_ = r.db.UpdateEventStatus(e.ID, storage.EventStatusFailed, "agent_buffer_full", true,
					r.cfg.MaxRetry, r.cfg.RetryInitialSecs, r.cfg.RetryMaxSecs)
			}
		}
	}
}

func (r *Relay) HandleAck(agentID, status string, eventID int64, retryable bool, reason string) {
	log.Printf("[relay] ack from agent=%s event=%d status=%s retryable=%t reason=%s",
		agentID, eventID, status, retryable, reason)

	client := r.cm.GetClient(agentID)
	if client != nil {
		client.SetLastAck(eventID)
	}

	var eventStatus storage.EventStatus
	switch status {
	case "ok":
		eventStatus = storage.EventStatusAcked
	case "failed":
		if retryable {
			eventStatus = storage.EventStatusFailed
		} else {
			eventStatus = storage.EventStatusDead
		}
	default:
		eventStatus = storage.EventStatusFailed
	}

	if err := r.db.UpdateEventStatus(eventID, eventStatus, reason, retryable,
		r.cfg.MaxRetry, r.cfg.RetryInitialSecs, r.cfg.RetryMaxSecs); err != nil {
		log.Printf("[relay] update event %d status error: %v", eventID, err)
	}

	_ = r.db.InsertEventLog(eventID, "info",
		fmt.Sprintf("ack: agent=%s status=%s retryable=%t reason=%s", agentID, status, retryable, reason))

	if client != nil {
		_ = r.db.UpdateAgentLastAck(agentID, eventID)
	}
}

type GithubEventMessage struct {
	Type               string          `json:"type"`
	EventID            int64           `json:"event_id"`
	DeliveryID         string          `json:"delivery_id"`
	GitHubEvent        string          `json:"github_event"`
	RepositoryFullName string          `json:"repository_full_name"`
	InstallationID     int64           `json:"installation_id"`
	ReceivedAt         string          `json:"received_at"`
	Payload            json.RawMessage `json:"payload"`
}

type HelloMessage struct {
	Type       string `json:"type"`
	AgentID    string `json:"agent_id"`
	LastAckID  int64  `json:"last_ack_id,omitempty"`
}

type HelloAckMessage struct {
	Type       string `json:"type"`
	OK         bool   `json:"ok"`
	ServerTime string `json:"server_time"`
}

type AckMessage struct {
	Type      string `json:"type"`
	EventID   int64  `json:"event_id"`
	Status    string `json:"status"`
	Retryable bool   `json:"retryable,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

type PingMessage struct {
	Type string `json:"type"`
	Time string `json:"time"`
}

type PongMessage struct {
	Type string `json:"type"`
	Time string `json:"time"`
}
