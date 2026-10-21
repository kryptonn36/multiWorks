package ops

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// SubmitRequest is decoded by each transport and validated exclusively by Service.
type SubmitRequest struct {
	ClientID string          `json:"client_id"`
	OpID     string          `json:"op_id"`
	BaseSeq  int64           `json:"base_seq"`
	Type     string          `json:"type"`
	Payload  json.RawMessage `json:"payload"`
}

type Operation struct {
	ID        uuid.UUID       `json:"id"`
	ProjectID uuid.UUID       `json:"project_id"`
	Seq       int64           `json:"seq"`
	ClientID  string          `json:"client_id"`
	OpID      string          `json:"op_id"`
	ActorID   uuid.UUID       `json:"actor_id"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}

type SubmitResponse struct {
	Operation  Operation `json:"operation"`
	Duplicate  bool      `json:"duplicate"`
	CurrentSeq int64     `json:"current_seq"`
}

type ReplayResponse struct {
	Operations []Operation `json:"operations"`
	CurrentSeq int64       `json:"current_seq"`
	HasMore    bool        `json:"has_more"`
}
