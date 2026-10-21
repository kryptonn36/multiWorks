package websocket

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = 25 * time.Second
	authCheckEvery = 5 * time.Second
	replayBatch    = 100
	maxMessageSize = 128 * 1024
)

type Handler struct {
	DB    *sql.DB
	Hub   *Hub
	Store *Store
}

func NewHandler(database *sql.DB, hub *Hub) *Handler {
	return &Handler{
		DB:    database,
		Hub:   hub,
		Store: NewStore(database),
	}
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     allowedOrigin,
}

func (h *Handler) Connect(c *gin.Context) {
	projectID, err := uuid.Parse(c.Param("projectID"))
	if err != nil || projectID == uuid.Nil {
		c.JSON(404, gin.H{"error": "project not found"})
		return
	}
	lastSeen := int64(0)
	if raw := c.Query("last_seen_seq"); raw != "" {
		lastSeen, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || lastSeen < 0 {
			c.JSON(400, gin.H{"error": "invalid last_seen_seq"})
			return
		}
	}
	if !allowedOrigin(c.Request) {
		c.JSON(403, gin.H{"error": "origin not allowed"})
		return
	}
	userID, err := h.consumeTicket(c.Request.Context(), projectID, c.Query("ticket"))
	if err != nil {
		c.JSON(401, gin.H{"error": "invalid or expired WebSocket ticket"})
		return
	}

	role, err := h.Store.Role(c.Request.Context(), projectID, userID)
	if errors.Is(err, sql.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "project not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not check project access"})
		return
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}

	client := &Client{
		ID:        uuid.New(),
		UserID:    userID,
		ProjectID: projectID,
		Role:      role,
		Send:      make(chan ServerMessage, ClientQueueSize),
	}
	h.Hub.Register(client)

	defer func() {
		h.Hub.Unregister(client)
		_ = conn.Close()
		h.broadcastPresence(projectID)
	}()

	h.broadcastPresence(projectID)

	go h.writePump(conn, client)

	if err := h.sendReplay(client, lastSeen); err != nil {
		return
	}

	h.readPump(conn, client)
}

func (h *Handler) sendReplay(
	client *Client,
	lastSeen int64,
) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	for {
		ops, currentSeq, hasMore, err := h.Store.Replay(
			ctx, client.ProjectID, client.UserID, lastSeen, replayBatch,
		)
		if err != nil {
			return err
		}

		raw, err := json.Marshal(ops)
		if err != nil {
			return err
		}

		select {
		case client.Send <- ServerMessage{
			Type:       "replay",
			Operations: raw,
			CurrentSeq: currentSeq,
			HasMore:    hasMore,
		}:
		case <-client.Done:
			return context.Canceled
		case <-ctx.Done():
			return ctx.Err()
		}

		if len(ops) > 0 {
			var last struct {
				Seq int64 `json:"seq"`
			}
			if err := json.Unmarshal(ops[len(ops)-1], &last); err != nil {
				return err
			}
			lastSeen = last.Seq
		}

		if !hasMore {
			return nil
		}
	}
}

func (h *Handler) readPump(conn *websocket.Conn, client *Client) {
	conn.SetReadLimit(maxMessageSize)
	_ = conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(pongWait))
		h.Hub.Touch(client)
		return nil
	})

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		_ = conn.SetReadDeadline(time.Now().Add(pongWait))
		if !h.authorized(client) {
			return
		}

		h.Hub.Touch(client)

		var msg ClientMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			h.sendError(client, "invalid JSON message")
			continue
		}

		switch msg.Type {
		case "heartbeat":
			h.Hub.Touch(client)
			h.sendPresence(client.ProjectID)

		case "view_scene":
			if msg.SceneID != "" {
				sceneID, err := uuid.Parse(msg.SceneID)
				if err != nil || !h.sceneBelongsToProject(client.ProjectID, sceneID) {
					h.sendError(client, "scene not found in this project")
					continue
				}
			}
			h.Hub.SetScene(client, msg.SceneID)
			h.broadcastPresence(client.ProjectID)

		case "resume":
			if msg.LastSeen < 0 {
				h.sendError(client, "last_seen_seq cannot be negative")
				continue
			}
			// Replay messages are enqueued to the writer, not written directly
			// from this goroutine.
			if err := h.sendReplay(client, msg.LastSeen); err != nil {
				return
			}

		case "submit_op":
			// Writes are deliberately rejected here until the shared operation
			// service is wired to this socket handler. This prevents bypassing
			// Stage 5 validation and role checks.
			canWrite, err := h.Store.CanWrite(context.Background(), client.ProjectID, client.UserID)
			if err != nil || !canWrite {
				h.sendError(client, "editor or owner role required")
				continue
			}
			h.sendError(client, "WebSocket operation submission is not enabled; submit through the authenticated REST operation endpoint")

		default:
			h.sendError(client, "unsupported message type")
		}
	}
}

func (h *Handler) writePump(conn *websocket.Conn, client *Client) {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()
	defer conn.Close()
	authTicker := time.NewTicker(authCheckEvery)
	defer authTicker.Stop()

	for {
		select {
		case <-client.Done:
			return
		case <-authTicker.C:
			if !h.authorized(client) {
				_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
				_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "project access revoked"))
				return
			}
		case msg := <-client.Send:
			_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := conn.WriteJSON(msg); err != nil {
				return
			}

		case <-ticker.C:
			_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (h *Handler) authorized(client *Client) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := h.Store.Role(ctx, client.ProjectID, client.UserID)
	if err != nil {
		return false
	}

	return true
}

func (h *Handler) sceneBelongsToProject(projectID, sceneID uuid.UUID) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var exists bool
	err := h.DB.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM scenes
			WHERE id = $1 AND project_id = $2
		)
	`, sceneID, projectID).Scan(&exists)
	return err == nil && exists
}

func (h *Handler) sendError(client *Client, message string) {
	select {
	case <-client.Done:
	case client.Send <- ServerMessage{Type: "error", Message: message}:
	default:
	}
}

func (h *Handler) broadcastPresence(projectID uuid.UUID) {
	h.Hub.Broadcast(projectID, ServerMessage{
		Type:  "presence",
		Users: h.Hub.Presence(projectID),
	})
}

func (h *Handler) sendPresence(projectID uuid.UUID) {
	h.broadcastPresence(projectID)
}

// Require an exact configured origin, or a local development hostname.
func allowedOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	if configured := os.Getenv("FRONTEND_ORIGINS"); configured != "" {
		for _, allowed := range strings.Split(configured, ",") {
			if origin == strings.TrimSpace(allowed) {
				return true
			}
		}
		return false
	}
	return u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"
}
