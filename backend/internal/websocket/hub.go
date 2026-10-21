package websocket

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	ClientQueueSize = 128
	PresenceTTL     = 45 * time.Second
)

type Client struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	ProjectID uuid.UUID
	Role      string

	Send          chan ServerMessage
	Done          chan struct{}
	Permissions   chan struct{}
	Replaying     bool
	LastQueuedSeq int64
	Pending       map[int64]ServerMessage

	SceneID  string
	LastSeen time.Time
}

type Hub struct {
	mu      sync.RWMutex
	clients map[uuid.UUID]map[*Client]struct{}
}

func NewHub() *Hub {
	return &Hub{
		clients: make(map[uuid.UUID]map[*Client]struct{}),
	}
}

func (h *Hub) Register(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.clients[c.ProjectID] == nil {
		h.clients[c.ProjectID] = make(map[*Client]struct{})
	}
	if c.Done == nil {
		c.Done = make(chan struct{})
	}
	if c.Permissions == nil {
		c.Permissions = make(chan struct{}, 1)
	}
	if c.Pending == nil {
		c.Pending = make(map[int64]ServerMessage)
	}
	h.clients[c.ProjectID][c] = struct{}{}
	c.LastSeen = time.Now()
}

func (h *Hub) Unregister(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	projectClients := h.clients[c.ProjectID]
	if projectClients == nil {
		return
	}

	if _, exists := projectClients[c]; exists {
		delete(projectClients, c)
		close(c.Done)
	}
	if len(projectClients) == 0 {
		delete(h.clients, c.ProjectID)
	}
}

func (h *Hub) Touch(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, exists := h.clients[c.ProjectID][c]; exists {
		c.LastSeen = time.Now()
	}
}

func (h *Hub) SetScene(c *Client, sceneID string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, exists := h.clients[c.ProjectID][c]; exists {
		c.SceneID = sceneID
		c.LastSeen = time.Now()
	}
}

func (h *Hub) Broadcast(projectID uuid.UUID, message ServerMessage) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for client := range h.clients[projectID] {
		if message.Type == "operation" {
			if message.Seq <= client.LastQueuedSeq {
				continue
			}
			client.Pending[message.Seq] = message
			if len(client.Pending) > ClientQueueSize {
				h.removeLocked(client)
				continue
			}
			if !client.Replaying {
				h.flushLocked(client)
			}
		} else if !h.queueLocked(client, message) {
			h.removeLocked(client)
		}
	}

	if len(h.clients[projectID]) == 0 {
		delete(h.clients, projectID)
	}
}

func (h *Hub) Presence(projectID uuid.UUID) []PresenceUser {
	now := time.Now()
	users := make(map[uuid.UUID]PresenceUser)

	h.mu.RLock()
	defer h.mu.RUnlock()

	for client := range h.clients[projectID] {
		if now.Sub(client.LastSeen) > PresenceTTL {
			continue
		}

		if previous, exists := users[client.UserID]; exists && !client.LastSeen.After(previous.LastSeen) {
			continue
		}
		users[client.UserID] = PresenceUser{
			UserID:    client.UserID,
			SceneID:   client.SceneID,
			Connected: true,
			LastSeen:  client.LastSeen,
		}
	}

	result := make([]PresenceUser, 0, len(users))
	for _, user := range users {
		result = append(result, user)
	}
	return result
}

func (h *Hub) Clients(projectID uuid.UUID) []*Client {
	h.mu.RLock()
	defer h.mu.RUnlock()

	result := make([]*Client, 0, len(h.clients[projectID]))
	for client := range h.clients[projectID] {
		result = append(result, client)
	}
	return result
}

// BeginReplay runs under the same lock as Broadcast, before reading the DB boundary.
func (h *Hub) BeginReplay(c *Client) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.clients[c.ProjectID][c]; !ok {
		return false
	}
	c.Replaying = true
	return true
}

func (h *Hub) FinishReplay(c *Client, boundary int64) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.clients[c.ProjectID][c]; !ok {
		return false
	}
	// Replay covers delayed publications at or below the captured boundary.
	for seq := range c.Pending {
		if seq <= boundary {
			delete(c.Pending, seq)
		}
	}
	c.LastQueuedSeq = boundary
	if !h.queueLocked(c, ServerMessage{Type: "replay_complete", BoundarySeq: &boundary}) {
		h.removeLocked(c)
		return false
	}
	c.Replaying = false
	return h.flushLocked(c)
}

func (h *Hub) queueLocked(c *Client, message ServerMessage) bool {
	select {
	case c.Send <- message:
		return true
	default:
		return false
	}
}

func (h *Hub) removeLocked(c *Client) {
	if _, ok := h.clients[c.ProjectID][c]; ok {
		delete(h.clients[c.ProjectID], c)
		close(c.Done)
	}
}

func (h *Hub) flushLocked(c *Client) bool {
	for {
		seq := c.LastQueuedSeq + 1
		message, ok := c.Pending[seq]
		if !ok {
			return true
		}
		if !h.queueLocked(c, message) {
			h.removeLocked(c)
			return false
		}
		delete(c.Pending, seq)
		c.LastQueuedSeq = seq
	}
}

// Coalesced signals cause the writer to read authoritative permissions again.
// This avoids stale role values when two committed changes notify out of order.
func (h *Hub) PermissionsChanged(projectID, userID uuid.UUID) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients[projectID] {
		if c.UserID == userID {
			select {
			case c.Permissions <- struct{}{}:
			default:
			}
		}
	}
}
