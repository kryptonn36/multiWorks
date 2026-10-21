package websocket

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type ClientMessage struct {
	Type      string          `json:"type"`
	LastSeen  int64           `json:"last_seen_seq,omitempty"`
	SceneID   string          `json:"scene_id,omitempty"`
	Operation json.RawMessage `json:"operation,omitempty"`
}

type ServerMessage struct {
	Asset             json.RawMessage `json:"asset,omitempty"`
	Proposal          json.RawMessage `json:"proposal,omitempty"`
	ReconnectRequired bool            `json:"reconnect_required,omitempty"`
	BoundarySeq       *int64          `json:"boundary_seq,omitempty"`
	Role              string          `json:"role,omitempty"`
	AccessRevoked     bool            `json:"access_revoked,omitempty"`
	Status            int             `json:"status,omitempty"`
	ClientID          string          `json:"client_id,omitempty"`
	OpID              string          `json:"op_id,omitempty"`
	Duplicate         *bool           `json:"duplicate,omitempty"`
	Type              string          `json:"type"`
	Seq               int64           `json:"seq,omitempty"`
	Operation         json.RawMessage `json:"operation,omitempty"`
	Operations        json.RawMessage `json:"operations,omitempty"`
	CurrentSeq        int64           `json:"current_seq,omitempty"`
	HasMore           bool            `json:"has_more,omitempty"`
	UserID            string          `json:"user_id,omitempty"`
	SceneID           string          `json:"scene_id,omitempty"`
	Users             []PresenceUser  `json:"users,omitempty"`
	Message           string          `json:"message,omitempty"`
}

type PresenceUser struct {
	UserID    uuid.UUID `json:"user_id"`
	SceneID   string    `json:"scene_id,omitempty"`
	Connected bool      `json:"connected"`
	LastSeen  time.Time `json:"last_seen"`
}

type Event struct {
	ProjectID uuid.UUID
	Message   ServerMessage
}
