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

	Send chan ServerMessage
	Done chan struct{}

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
		select {
		case client.Send <- message:
		default:
			// Slow clients must reconnect and replay rather than
			// blocking other collaborators.
			delete(h.clients[projectID], client)
			close(client.Done)
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
