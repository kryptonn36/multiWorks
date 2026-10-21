package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/kryptonn36/multiWorks/internal/db"

	"github.com/google/uuid"
	"github.com/kryptonn36/multiWorks/internal/board"
	"github.com/kryptonn36/multiWorks/internal/ops"
)

type Service struct {
	repository *Repository
	operations *ops.Service
}

func NewService(repository *Repository, operations *ops.Service) *Service {
	service := &Service{repository: repository, operations: operations}
	operations.SetRestoreLoader(func(ctx context.Context, tx *sql.Tx, project uuid.UUID, seq int64) (board.State, error) {
		return repository.Reconstruct(ctx, tx, project, seq)
	})
	return service
}

type failure struct {
	status  int
	message string
}

func (e *failure) Error() string { return e.message }
func status(err error) int {
	var f *failure
	if errors.As(err, &f) {
		return f.status
	}
	if errors.Is(err, sql.ErrNoRows) {
		return 404
	}
	if errors.Is(err, ErrUnavailable) {
		return 409
	}
	return ops.ErrorStatus(err)
}

type Listing struct {
	ProjectID  uuid.UUID           `json:"project_id"`
	CurrentSeq int64               `json:"current_seq"`
	Snapshots  []Snapshot          `json:"snapshots"`
	Operations []OperationMetadata `json:"operations"`
}

func (s *Service) List(ctx context.Context, project, user uuid.UUID, before int64) (Listing, error) {
	var result Listing
	tx, err := s.repository.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	current, _, err := s.repository.Access(ctx, tx, project, user)
	if err != nil {
		return result, err
	}
	snapshots, operations, err := s.repository.List(ctx, tx, project, before)
	if err != nil {
		return result, err
	}
	result = Listing{ProjectID: project, CurrentSeq: current, Snapshots: snapshots, Operations: operations}
	return result, tx.Commit()
}
func (s *Service) GetAtSequence(ctx context.Context, project, user uuid.UUID, seq int64) (board.State, error) {
	var result board.State
	tx, err := s.repository.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	current, _, err := s.repository.Access(ctx, tx, project, user)
	if err != nil {
		return result, err
	}
	if seq < 0 || seq > current {
		return result, &failure{400, "sequence must be between zero and the current project sequence"}
	}
	result, err = s.repository.Reconstruct(ctx, tx, project, seq)
	if err != nil {
		return result, err
	}
	return result, tx.Commit()
}
func (s *Service) Restore(ctx context.Context, project, user uuid.UUID, seq int64, request ops.SubmitRequest) (ops.SubmitResponse, error) {
	raw, _ := json.Marshal(struct {
		Seq int64 `json:"seq"`
	}{seq})
	request.Type = "restore_board"
	request.Payload = raw
	return s.operations.Submit(ctx, project, user, request)
}
func (s *Service) CreateSnapshot(ctx context.Context, project, user uuid.UUID) error {
	tx, err := s.repository.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, lockProjectErr := db.New(tx).LockProject(ctx, project)
	if err := lockProjectErr; err != nil {
		return err
	}
	seq, role, err := s.repository.Access(ctx, tx, project, user)
	if err != nil {
		return err
	}
	if role != "owner" && role != "editor" {
		return &failure{403, "editor or owner role required"}
	}
	if err := board.SaveSnapshot(ctx, tx, project, user, seq); err != nil {
		return err
	}
	return tx.Commit()
}
