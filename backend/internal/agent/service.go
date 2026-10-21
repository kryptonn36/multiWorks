package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/kryptonn36/multiWorks/internal/board"
	"github.com/kryptonn36/multiWorks/internal/db"
	"github.com/kryptonn36/multiWorks/internal/ops"
)

type Service struct {
	Limits     Limits
	Database   *sql.DB
	Operations *ops.Service
	Publish    func(uuid.UUID, Proposal)
}

func NewService(database *sql.DB, operations *ops.Service, configured ...Limits) *Service {
	limits := Limits{MaxOps: ops.MaxProposalOperations, Timeout: 15 * time.Second}
	if len(configured) > 0 {
		limits = configured[0]
	}
	return &Service{Database: database, Operations: operations, Limits: limits}
}
func (s *Service) access(ctx context.Context, project, user uuid.UUID, write bool) error {
	if user == uuid.Nil {
		return ops.NewError(401, "authentication required")
	}
	row, err := db.New(s.Database).GetHistoryAccess(ctx, db.GetHistoryAccessParams{ProjectID: project, UserID: user})
	if errors.Is(err, sql.ErrNoRows) {
		return ops.NewError(404, "project not found")
	}
	if err != nil {
		return err
	}
	if write && row.Role != "owner" && row.Role != "editor" {
		return ops.NewError(403, "editor or owner role required")
	}
	return nil
}
func (s *Service) publish(p Proposal) {
	if s.Publish != nil {
		s.Publish(p.ProjectID, p)
	}
}
func (s *Service) Generate(ctx context.Context, project, user uuid.UUID, instruction, mode string) (Proposal, error) {
	state, mode, err := s.capture(ctx, project, user, instruction, mode)
	if err != nil {
		return Proposal{}, err
	}
	proposed, reason := s.generate(ctx, state, instruction, mode)
	status := "pending"
	if reason != "" {
		status = "failed"
	}

	var row db.AgentProposal
	_, err = s.Operations.Transact(ctx, project, user, func(tx *sql.Tx, _ func(ops.SubmitRequest) (ops.SubmitResponse, error)) error {
		queries := db.New(tx)
		if status == "pending" {
			why, err := checkNewIDs(ctx, queries, proposed)
			if err != nil {
				return err
			}
			if why != "" {
				reason = why
				status = "failed"
				proposed = json.RawMessage(`[]`)
			}
		}
		var err error
		row, err = queries.CreateAgentProposal(ctx, db.CreateAgentProposalParams{ProjectID: project, RequestedBy: uuid.NullUUID{UUID: user, Valid: true}, Instruction: instruction, GenerationMode: mode, BaseSeq: state.LastSeq, Status: status, ProposedOps: proposed, FailureReason: sql.NullString{String: reason, Valid: reason != ""}})
		return err
	})
	if err != nil {
		return Proposal{}, err
	}
	p := present(row)
	s.publish(p)
	return p, nil
}
func (s *Service) List(ctx context.Context, project, user, after uuid.UUID) ([]Proposal, bool, error) {
	if err := s.access(ctx, project, user, false); err != nil {
		return nil, false, err
	}
	rows, err := db.New(s.Database).ListAgentProposals(ctx, db.ListAgentProposalsParams{ProjectID: project, AfterID: after})
	if err != nil {
		return nil, false, err
	}
	more := len(rows) > 100
	if more {
		rows = rows[:100]
	}
	items := make([]Proposal, 0, len(rows))
	for _, row := range rows {
		items = append(items, present(row))
	}
	return items, more, nil
}
func (s *Service) Get(ctx context.Context, project, user, id uuid.UUID) (Proposal, error) {
	if err := s.access(ctx, project, user, false); err != nil {
		return Proposal{}, err
	}
	row, err := db.New(s.Database).GetAgentProposal(ctx, db.GetAgentProposalParams{ProjectID: project, ID: id})
	if errors.Is(err, sql.ErrNoRows) {
		return Proposal{}, ops.NewError(404, "proposal not found")
	}
	return present(row), err
}
func (s *Service) Decide(ctx context.Context, project, user, id uuid.UUID, approve bool) (Proposal, error) {
	var row db.AgentProposal
	stale := false
	invalid := false
	changed := false
	_, err := s.Operations.Transact(ctx, project, user, func(tx *sql.Tx, apply func(ops.SubmitRequest) (ops.SubmitResponse, error)) error {
		queries := db.New(tx)
		var err error
		row, err = queries.LockAgentProposal(ctx, db.LockAgentProposalParams{ProjectID: project, ID: id})
		if errors.Is(err, sql.ErrNoRows) {
			return ops.NewError(404, "proposal not found")
		}
		if err != nil {
			return err
		}
		if approve && row.Status == "approved" || !approve && row.Status == "rejected" {
			return nil
		}
		if row.Status != "pending" {
			return ops.NewError(409, "proposal is already "+row.Status)
		}
		status := "rejected"
		reason := ""
		applied := []ops.Operation{}
		if approve {
			current, err := queries.GetHistoryAccess(ctx, db.GetHistoryAccessParams{ProjectID: project, UserID: user})
			if err != nil {
				return err
			}
			if current.LastSeq != row.BaseSeq {
				status = "stale"
				reason = "The board changed. Generate a new proposal before approving."
				stale = true
			} else {
				budget, err := queries.AgentContextBudget(ctx, project)
				if err != nil {
					return err
				}
				if budget > MaxContextBytes {
					return ops.NewError(400, "board context exceeds the 256 KiB limit")
				}
				state, err := board.Load(ctx, tx, project)
				if err != nil {
					return err
				}

				var proposed []ops.ProposedOperation
				validationErr := json.Unmarshal(row.ProposedOps, &proposed)
				if validationErr == nil {
					validationErr = ops.ValidateProposal(state, proposed, s.Limits.MaxOps)
				}
				if validationErr != nil {
					status = "failed"
					reason = "Stored proposal failed validation. Generate a new proposal."
					invalid = true
				} else {
					for index, operation := range proposed {
						result, err := apply(ops.SubmitRequest{ClientID: "agent:" + id.String(), OpID: fmt.Sprint(index), BaseSeq: row.BaseSeq + int64(index), Type: operation.Type, Payload: operation.Payload})
						if err != nil {
							return err
						}
						applied = append(applied, result.Operation)
					}
					status = "approved"
				}

			}
		}
		raw, _ := json.Marshal(applied)
		row, err = queries.DecideAgentProposal(ctx, db.DecideAgentProposalParams{Status: status, AppliedOps: raw, FailureReason: sql.NullString{String: reason, Valid: reason != ""}, ProjectID: project, ID: id})
		changed = err == nil
		return err
	})
	if err != nil {
		return Proposal{}, err
	}
	p := present(row)
	if changed {
		s.publish(p)
	}
	if stale || invalid {
		return p, ops.NewError(409, p.FailureReason)
	}
	return p, nil
}
