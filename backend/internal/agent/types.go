package agent

import (
	"encoding/json"
	"github.com/google/uuid"
	"github.com/kryptonn36/multiWorks/internal/db"
	"github.com/kryptonn36/multiWorks/internal/ops"
	"time"
)

const MaxContextBytes = 256 * 1024

type Limits struct {
	MaxOps  int
	Timeout time.Duration
}

type ProposeRequest struct {
	Instruction string `json:"instruction"`
	Mode        string `json:"mode"`
}

type ProposedOperation = ops.ProposedOperation

type Proposal struct {
	ID                uuid.UUID               `json:"id"`
	ProjectID         uuid.UUID               `json:"project_id"`
	RequestedBy       uuid.NullUUID           `json:"requested_by"`
	Instruction       string                  `json:"instruction"`
	Mode              string                  `json:"mode"`
	BaseSeq           int64                   `json:"base_seq"`
	Status            string                  `json:"status"`
	Operations        []ops.ProposedOperation `json:"proposed_ops"`
	AppliedOperations []ops.Operation         `json:"applied_operations"`
	FailureReason     string                  `json:"failure_reason,omitempty"`
	CreatedAt         time.Time               `json:"created_at"`
	UpdatedAt         time.Time               `json:"updated_at"`
}

func present(row db.AgentProposal) Proposal {
	p := Proposal{ID: row.ID, ProjectID: row.ProjectID, RequestedBy: row.RequestedBy, Instruction: row.Instruction, Mode: row.GenerationMode, BaseSeq: row.BaseSeq, Status: row.Status, FailureReason: row.FailureReason.String, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, Operations: []ops.ProposedOperation{}, AppliedOperations: []ops.Operation{}}
	_ = json.Unmarshal(row.ProposedOps, &p.Operations)
	_ = json.Unmarshal(row.AppliedOps, &p.AppliedOperations)
	return p
}
