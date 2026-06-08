package websocket

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/nkit/github-webhook-relay/internal/relay"
	"github.com/nkit/github-webhook-relay/internal/storage"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type Handler struct {
	token      string
	db         *storage.DB
	relay      *relay.Relay
	readTimeout  time.Duration
	writeTimeout time.Duration
	pingInterval time.Duration
}

func NewHandler(token string, db *storage.DB, r *relay.Relay, readTimeout, writeTimeout, pingInterval time.Duration) *Handler {
	return &Handler{
		token:       token,
		db:          db,
		relay:       r,
		readTimeout:  readTimeout,
		writeTimeout: writeTimeout,
		pingInterval: pingInterval,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	token := extractToken(r, h.token)
	if token != h.token || token == "" {
		log.Printf("[ws] auth failed from %s", r.RemoteAddr)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[ws] upgrade error from %s: %v", r.RemoteAddr, err)
		return
	}

	log.Printf("[ws] connected from %s", r.RemoteAddr)
	h.handleConnection(conn)
}

func extractToken(r *http.Request, _ string) string {
	if auth := r.Header.Get("Authorization"); auth != "" {
		const prefix = "Bearer "
		if strings.HasPrefix(auth, prefix) {
			return auth[len(prefix):]
		}
	}
	if token := r.URL.Query().Get("token"); token != "" {
		return token
	}
	return ""
}

func (h *Handler) handleConnection(conn *websocket.Conn) {
	defer conn.Close()

	conn.SetReadDeadline(time.Now().Add(h.readTimeout))

	var helloMsg relay.HelloMessage
	_, raw, err := conn.ReadMessage()
	if err != nil {
		log.Printf("[ws] read hello error: %v", err)
		return
	}

	if err := json.Unmarshal(raw, &helloMsg); err != nil || helloMsg.Type != "hello" || helloMsg.AgentID == "" {
		log.Printf("[ws] invalid hello message")
		return
	}

	log.Printf("[ws] agent %s connected (last_ack=%d)", helloMsg.AgentID, helloMsg.LastAckID)

	_ = h.db.UpsertAgent(helloMsg.AgentID)

	agentID := helloMsg.AgentID
	client := h.relay.ClientManager().Register(agentID)
	defer func() {
		h.relay.ClientManager().Unregister(agentID)
		_ = h.db.DisconnectAgent(agentID)
		log.Printf("[ws] agent %s disconnected", agentID)
	}()

	helloAck := relay.HelloAckMessage{
		Type:       "hello_ack",
		OK:         true,
		ServerTime: time.Now().UTC().Format(time.RFC3339),
	}
	helloAckData, _ := json.Marshal(helloAck)
	conn.SetWriteDeadline(time.Now().Add(h.writeTimeout))
	if err := conn.WriteMessage(websocket.TextMessage, helloAckData); err != nil {
		log.Printf("[ws] write hello_ack error: %v", err)
		return
	}

	h.relay.NotifyNewEvent()

	done := make(chan struct{})
	pingTicker := time.NewTicker(h.pingInterval)
	defer pingTicker.Stop()

	go func() {
		defer close(done)
		for {
			conn.SetReadDeadline(time.Now().Add(h.readTimeout))
			_, raw, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
					log.Printf("[ws] read error: %v", err)
				}
				return
			}

			var base struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(raw, &base); err != nil {
				log.Printf("[ws] invalid message from %s: %v", agentID, err)
				continue
			}

			switch base.Type {
			case "ping":
				var ping relay.PingMessage
				if err := json.Unmarshal(raw, &ping); err == nil {
					pong := relay.PongMessage{
						Type: "pong",
						Time: time.Now().UTC().Format(time.RFC3339),
					}
					pongData, _ := json.Marshal(pong)
					conn.SetWriteDeadline(time.Now().Add(h.writeTimeout))
					if err := conn.WriteMessage(websocket.TextMessage, pongData); err != nil {
						return
					}
				}

			case "ack":
				var ack relay.AckMessage
				if err := json.Unmarshal(raw, &ack); err == nil {
					h.relay.HandleAck(agentID, ack.Status, ack.EventID, ack.Retryable, ack.Reason)
				}

			default:
				log.Printf("[ws] unknown message type from %s: %s", agentID, base.Type)
			}
		}
	}()

	for {
		select {
		case <-done:
			return
		case data, ok := <-client.SendCh:
			if !ok {
				return
			}
			conn.SetWriteDeadline(time.Now().Add(h.writeTimeout))
			if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
				log.Printf("[ws] write event to %s error: %v", agentID, err)
				return
			}
		case <-pingTicker.C:
			conn.SetWriteDeadline(time.Now().Add(h.writeTimeout))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				log.Printf("[ws] ping error to %s: %v", agentID, err)
				return
			}
		}
	}
}
