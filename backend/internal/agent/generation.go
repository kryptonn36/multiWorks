package agent

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/kryptonn36/multiWorks/internal/board"
	"github.com/kryptonn36/multiWorks/internal/db"
	"github.com/kryptonn36/multiWorks/internal/ops"
)

func (s *Service) capture(ctx context.Context, project, user uuid.UUID, instruction, mode string) (board.State, string, error) {
	var state board.State
	if err := s.access(ctx, project, user, true); err != nil {
		return state, mode, err
	}
	if !utf8.ValidString(instruction) || strings.TrimSpace(instruction) == "" || utf8.RuneCountInString(instruction) > 2000 {
		return state, mode, ops.NewError(400, "instruction must contain 1-2000 characters")
	}
	mode = mockMode(instruction, mode)
	switch mode {
	case "normal", "malformed", "invalid", "oversized", "delayed", "timeout", "missing-shot", "too-many":
	default:
		return state, mode, ops.NewError(400, "unknown mock mode")
	}
	tx, err := s.Database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return state, mode, err
	}
	defer tx.Rollback()
	// Both size check and board load use one PostgreSQL snapshot, including last_seq.
	budget, err := db.New(tx).AgentContextBudget(ctx, project)
	if err != nil {
		return state, mode, err
	}
	if budget > MaxContextBytes {
		return state, mode, ops.NewError(400, "board context exceeds the 256 KiB limit; use a smaller board")
	}
	state, err = board.Load(ctx, tx, project)
	if err != nil {
		return state, mode, err
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return state, mode, err
	}
	if len(raw) > MaxContextBytes {
		return state, mode, ops.NewError(400, "board context exceeds the 256 KiB limit")
	}
	return state, mode, tx.Commit()
}

func (s *Service) generate(ctx context.Context, state board.State, instruction, mode string) (json.RawMessage, string) {
	generationCtx, cancel := context.WithTimeout(ctx, s.Limits.Timeout)
	defer cancel()
	raw, err := Mock(generationCtx, state, instruction, mode, s.Limits.MaxOps)
	if err != nil {
		return json.RawMessage(`[]`), "Mock agent timed out. Try another proposal."
	}
	proposed, err := DecodeResponse(state, raw, s.Limits.MaxOps)
	if err != nil {
		return json.RawMessage(`[]`), "Agent proposal failed validation: " + err.Error()
	}
	return proposed, ""
}

// DecodeResponse is also the boundary for any future provider. No output is
// executable until size/depth, strict schema, and project references are checked.
func DecodeResponse(state board.State, raw []byte, maxOps ...int) (json.RawMessage, error) {
	if err := ops.ValidateProposalJSON(raw); err != nil {
		return nil, err
	}
	var response struct {
		Operations []ops.ProposedOperation `json:"operations"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		return nil, ops.NewError(400, "agent returned malformed JSON or an unknown field")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, ops.NewError(400, "agent returned trailing JSON")
	}
	if err := ops.ValidateProposal(state, response.Operations, maxOps...); err != nil {
		return nil, err
	}
	proposed, err := json.Marshal(response.Operations)
	return proposed, err
}

// Propose commits a durable job and returns before invoking the mock provider.
func (s *Service) Propose(ctx context.Context, project, user uuid.UUID, instruction, mode string) (Proposal, error) {
	state, mode, err := s.capture(ctx, project, user, instruction, mode)
	if err != nil {
		return Proposal{}, err
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return Proposal{}, err
	}
	var row db.AgentProposal
	_, err = s.Operations.Transact(ctx, project, user, func(tx *sql.Tx, _ func(ops.SubmitRequest) (ops.SubmitResponse, error)) error {
		queries := db.New(tx)
		var err error
		row, err = queries.CreateAgentProposal(ctx, db.CreateAgentProposalParams{ProjectID: project, RequestedBy: uuid.NullUUID{UUID: user, Valid: true}, Instruction: instruction, GenerationMode: mode, BaseSeq: state.LastSeq, Status: "generating", ProposedOps: json.RawMessage(`[]`)})
		if err != nil {
			return err
		}
		return queries.EnqueueAgentJob(ctx, db.EnqueueAgentJobParams{ProposalID: row.ID, BoardContext: raw})
	})
	if err != nil {
		return Proposal{}, err
	}
	p := present(row)
	s.publish(p)
	return p, nil
}

// Creation IDs must also be free globally, rather than merely absent from the
// selected project's context. The operation engine checks them again at commit.
func checkNewIDs(ctx context.Context, queries *db.Queries, raw json.RawMessage) (string, error) {
	var proposed []ops.ProposedOperation
	if err := json.Unmarshal(raw, &proposed); err != nil {
		return "Stored proposal is invalid.", nil
	}
	for _, operation := range proposed {
		var p struct {
			SceneID uuid.UUID `json:"scene_id"`
			ShotID  uuid.UUID `json:"shot_id"`
		}
		if err := json.Unmarshal(operation.Payload, &p); err != nil {
			return "Stored proposal is invalid.", nil
		}
		var exists bool
		var err error
		switch operation.Type {
		case "scene.create":
			exists, err = queries.AgentSceneIDExists(ctx, p.SceneID)
		case "shot.create":
			exists, err = queries.AgentShotIDExists(ctx, p.ShotID)
		}
		if err != nil {
			return "", err
		}
		if exists {
			return "Agent proposal uses an existing object ID.", nil
		}
	}
	return "", nil
}
