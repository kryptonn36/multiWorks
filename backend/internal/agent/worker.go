package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"time"

	"github.com/kryptonn36/multiWorks/internal/board"
	"github.com/kryptonn36/multiWorks/internal/db"
)

func (s *Service) Run(ctx context.Context) {
	for ctx.Err() == nil {
		processed, err := s.ProcessOne(ctx)
		if err != nil && ctx.Err() == nil {
			log.Printf("agent worker: %v", err)
		}
		if !processed || err != nil {
			timer := time.NewTimer(250 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}
}

func (s *Service) ProcessOne(parent context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(parent, s.Limits.Timeout+5*time.Second)
	defer cancel()
	queries := db.New(s.Database)
	job, err := queries.ClaimAgentJob(ctx, max(30, int64((s.Limits.Timeout+time.Second-1)/time.Second)+15))
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	row, err := queries.GetAgentJobProposal(ctx, job.ProposalID)
	if err != nil {
		return true, err
	}
	reason := ""
	var state board.State
	proposed := json.RawMessage(`[]`)
	if job.Attempts > 3 {
		reason = "Agent generation was interrupted too often. Generate a new proposal."
	} else if !row.RequestedBy.Valid || s.access(ctx, row.ProjectID, row.RequestedBy.UUID, true) != nil {
		reason = "Project access changed. Generate a new proposal with editor access."
	} else if len(job.BoardContext) > MaxContextBytes || json.Unmarshal(job.BoardContext, &state) != nil || state.LastSeq != row.BaseSeq || board.Validate(state, row.ProjectID) != nil {
		reason = "Stored board context is invalid."
	} else {
		proposed, reason = s.generate(ctx, state, row.Instruction, row.GenerationMode)
	}
	// Leave an interrupted job leased for recovery after a process restart.
	if parent.Err() != nil {
		return true, parent.Err()
	}
	tx, err := s.Database.BeginTx(ctx, nil)
	if err != nil {
		return true, err
	}
	defer tx.Rollback()
	queries = db.New(tx)
	if _, err = queries.LockOwnedAgentJob(ctx, db.LockOwnedAgentJobParams{ProposalID: job.ProposalID, Attempts: job.Attempts}); errors.Is(err, sql.ErrNoRows) {
		return true, nil
	} else if err != nil {
		return true, err
	}
	if reason == "" {
		why, err := checkNewIDs(ctx, queries, proposed)
		if err != nil {
			return true, err
		}
		if why != "" {
			reason = why
			proposed = json.RawMessage(`[]`)
		}
	}
	status := "pending"
	if reason != "" {
		status = "failed"
	}
	row, err = queries.FinishAgentProposal(ctx, db.FinishAgentProposalParams{ID: row.ID, Status: status, ProposedOps: proposed, FailureReason: sql.NullString{String: reason, Valid: reason != ""}})
	changed := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return true, err
	}
	if err = queries.CompleteAgentJob(ctx, job.ProposalID); err != nil {
		return true, err
	}
	if err = tx.Commit(); err != nil {
		return true, err
	}
	if changed {
		s.publish(present(row))
	}
	return true, nil
}
