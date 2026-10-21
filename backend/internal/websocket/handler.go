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
	"github.com/kryptonn36/multiWorks/internal/ops"
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
	DB         *sql.DB
	Hub        *Hub
	Store      *Store
	Operations *ops.Service
}

func NewHandler(database *sql.DB, hub *Hub, operations *ops.Service) *Handler {
	return &Handler{
		DB:         database,
		Hub:        hub,
		Store:      NewStore(database),
		Operations: operations,
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
		Replaying: true,
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

	if !h.Hub.BeginReplay(client) {
		return context.Canceled
	}
	boundary, err := h.Store.Boundary(ctx, client.ProjectID, client.UserID)
	if err != nil {
		return err
	}
	if lastSeen > boundary {
		return errors.New("last_seen_seq is ahead of the project")
	}
	select {
	case client.Send <- ServerMessage{Type: "replay_start", BoundarySeq: &boundary}:
	case <-client.Done:
		return context.Canceled
	case <-ctx.Done():
		return ctx.Err()
	}
	for {
		ops, currentSeq, hasMore, err := h.Store.ReplayThrough(
			ctx, client.ProjectID, client.UserID, lastSeen, replayBatch, boundary,
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
			Type:        "replay",
			BoundarySeq: &boundary,
			Operations:  raw,
			CurrentSeq:  currentSeq,
			HasMore:     hasMore,
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
			if lastSeen != boundary {
				return errors.New("operation history does not cover replay boundary")
			}
			if !h.Hub.FinishReplay(client, boundary) {
				return context.Canceled
			}
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
			h.submitOperation(client, msg.Operation)

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

	role := client.Role
	refresh := func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		current, err := h.Store.Role(ctx, client.ProjectID, client.UserID)
		_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseTryAgainLater, "permission check unavailable"))
				return false
			}
			_ = conn.WriteJSON(ServerMessage{Type: "permissions", AccessRevoked: true, Message: "Project access was revoked. Reopen the project after access is restored."})
			_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "project access revoked"))
			return false
		}
		if current != role {
			role = current
			if err := conn.WriteJSON(ServerMessage{Type: "permissions", Role: role, Message: "Project role changed to " + role + ". Write permissions are checked by the backend."}); err != nil {
				return false
			}
		}
		return true
	}
	if !refresh() {
		return
	}
	for {
		select {
		case <-client.Done:
			return
		case <-client.Permissions:
			if !refresh() {
				return
			}
		case <-authTicker.C:
			if !refresh() {
				return
			}
		case msg := <-client.Send:
			select {
			case <-client.Permissions:
				if !refresh() {
					return
				}
			default:
			}
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

// submitOperation delegates all business checks to the same service used by REST.
func (h *Handler) submitOperation(client *Client, raw json.RawMessage) {
	var request ops.SubmitRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		h.enqueue(client, ServerMessage{Type: "error", Status: 400, Message: "invalid operation request"})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := h.Operations.Submit(ctx, client.ProjectID, client.UserID, request)
	if err != nil {
		h.enqueue(client, ServerMessage{Type: "error", Status: ops.ErrorStatus(err), Message: err.Error(), ClientID: request.ClientID, OpID: request.OpID})
		return
	}
	operation, err := json.Marshal(result.Operation)
	if err != nil {
		h.sendError(client, "could not encode operation acknowledgement")
		return
	}
	// Publication already happened after commit. This targeted ack also confirms duplicates.
	h.enqueue(client, ServerMessage{Type: "ack", Operation: operation, Seq: result.Operation.Seq, CurrentSeq: result.CurrentSeq, Duplicate: &result.Duplicate, ClientID: request.ClientID, OpID: request.OpID})
}

func (h *Handler) enqueue(client *Client, message ServerMessage) {
	timer := time.NewTimer(writeWait)
	defer timer.Stop()
	select {
	case <-client.Done:
	case client.Send <- message:
	case <-timer.C:
		// Stop a blocked writer; reconnect/replay and the original idempotency key recover.
		h.Hub.Unregister(client)
	}
}

// Workspace handlers call this only after their membership write commits.
func (h *Handler) WorkspacePermissionsChanged(workspaceID, userID uuid.UUID) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	rows, err := h.DB.QueryContext(ctx, `SELECT id FROM projects WHERE workspace_id=$1`, workspaceID)
	if err != nil {
		return
	} // The periodic check remains the fallback.
	defer rows.Close()
	for rows.Next() {
		var projectID uuid.UUID
		if rows.Scan(&projectID) == nil {
			h.Hub.PermissionsChanged(projectID, userID)
		}
	}
}
