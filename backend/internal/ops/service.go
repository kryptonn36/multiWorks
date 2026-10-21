package ops

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/kryptonn36/multiWorks/internal/db"
	"net/http"

	"github.com/google/uuid"
	"github.com/kryptonn36/multiWorks/internal/board"
)

// Service is the single transaction and publication pipeline for all transports.
// Configure the publisher before serving requests.
type Service struct {
	database      *sql.DB
	restoreLoader func(context.Context, *sql.Tx, uuid.UUID, int64) (board.State, error)
	publisher     func(uuid.UUID, Operation)
}

func NewService(database *sql.DB) *Service                           { return &Service{database: database} }
func (s *Service) SetPublisher(publisher func(uuid.UUID, Operation)) { s.publisher = publisher }

// SetRestoreLoader supplies server-side reconstruction; client board data is never trusted.
func (s *Service) SetRestoreLoader(loader func(context.Context, *sql.Tx, uuid.UUID, int64) (board.State, error)) {
	s.restoreLoader = loader
}

type restoreIntent struct {
	Seq *int64 `json:"seq"`
}

func parseRestoreIntent(raw json.RawMessage) (restoreIntent, error) {
	var intent restoreIntent
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	err := decoder.Decode(&intent)
	if err != nil || intent.Seq == nil || *intent.Seq < 0 {
		return intent, opError(400, "restore payload must contain only a non-negative seq")
	}
	return intent, nil
}
func matchingPayload(kind string, existing, incoming json.RawMessage) bool {
	if kind != "restore_board" {
		return jsonEqual(existing, incoming)
	}
	var stored restoreIntent
	intent, err := parseRestoreIntent(incoming)
	return err == nil && json.Unmarshal(existing, &stored) == nil && stored.Seq != nil && *stored.Seq == *intent.Seq
}

// ErrorStatus maps a service failure to the same status for REST and socket errors.
func ErrorStatus(err error) int {
	var failure *operationError
	if errors.As(err, &failure) {
		return failure.status
	}
	return http.StatusInternalServerError
}

func (s *Service) Submit(ctx context.Context, projectID, userID uuid.UUID, req SubmitRequest) (SubmitResponse, error) {
	if userID == uuid.Nil {
		return SubmitResponse{}, opError(http.StatusUnauthorized, "authentication required")
	}
	if projectID == uuid.Nil {
		return SubmitResponse{}, opError(http.StatusNotFound, "project not found")
	}
	if err := validateRequest(req); err != nil {
		return SubmitResponse{}, opError(http.StatusBadRequest, err.Error())
	}

	_, err := decodePayload(req.Payload)
	if err != nil {
		return SubmitResponse{}, opError(http.StatusBadRequest, "invalid operation payload")
	}

	var result SubmitResponse
	_, err = s.Transact(ctx, projectID, userID, func(tx *sql.Tx, apply func(SubmitRequest) (SubmitResponse, error)) error {
		var err error
		result, err = apply(req)
		return err
	})
	return result, err
}

// Transact keeps preparation, a bounded batch, and its decision metadata atomic.
// Only committed, non-duplicate operations are published.
func (s *Service) Transact(ctx context.Context, projectID, userID uuid.UUID, run func(*sql.Tx, func(SubmitRequest) (SubmitResponse, error)) error) ([]SubmitResponse, error) {
	if userID == uuid.Nil {
		return nil, opError(401, "authentication required")
	}
	tx, err := s.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, opError(500, "could not start operation")
	}
	defer tx.Rollback()
	if _, err := db.New(tx).LockProjectForOperation(ctx, db.LockProjectForOperationParams{ProjectID: projectID, UserID: userID}); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, opError(404, "project not found")
		}
		return nil, err
	}
	role, err := projectRoleTx(ctx, tx, projectID, userID)
	if err != nil {
		return nil, err
	}
	if role != "owner" && role != "editor" {
		return nil, opError(403, "editor or owner role required")
	}
	results := []SubmitResponse{}
	apply := func(req SubmitRequest) (SubmitResponse, error) {
		result, err := s.submitTx(ctx, tx, projectID, userID, req)
		if err == nil {
			results = append(results, result)
		}
		return result, err
	}
	if err := run(tx, apply); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, opError(500, "could not commit operation")
	}
	for _, result := range results {
		if !result.Duplicate && s.publisher != nil {
			s.publisher(projectID, result.Operation)
		}
	}
	return results, nil
}

func NewError(status int, message string) error { return opError(status, message) }

func (s *Service) submitTx(ctx context.Context, tx *sql.Tx, projectID, userID uuid.UUID, req SubmitRequest) (SubmitResponse, error) {
	if err := validateRequest(req); err != nil {
		return SubmitResponse{}, opError(400, err.Error())
	}
	p, err := decodePayload(req.Payload)
	if err != nil {
		return SubmitResponse{}, opError(400, "invalid operation payload")
	}
	queries := db.New(s.database).WithTx(tx)

	// Lock this project row. All writers for this project serialize here.
	currentSeq, err := queries.LockProjectForOperation(ctx, db.LockProjectForOperationParams{ProjectID: projectID, UserID: userID})

	if errors.Is(err, sql.ErrNoRows) {
		return SubmitResponse{}, opError(http.StatusNotFound, "project not found")
	}
	if err != nil {
		return SubmitResponse{}, opError(http.StatusInternalServerError, "could not load project")
	}

	role, err := projectRoleTx(ctx, tx, projectID, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return SubmitResponse{}, opError(http.StatusNotFound, "project not found")
	}
	if err != nil {
		return SubmitResponse{}, opError(http.StatusInternalServerError, "could not check project permissions")
	}
	if role != "owner" && role != "editor" {
		return SubmitResponse{}, opError(http.StatusForbidden, "editor or owner role required")
	}

	// Check deduplication before applying any mutation.
	var existing Operation
	var existingPayload []byte
	findOperationResult, err := queries.FindOperation(ctx, db.FindOperationParams{ProjectID: projectID, ClientID: req.ClientID, OpID: req.OpID})
	existing = Operation{ID: findOperationResult.ID, ProjectID: findOperationResult.ProjectID, Seq: findOperationResult.Seq, ClientID: findOperationResult.ClientID, OpID: findOperationResult.OpID, ActorID: findOperationResult.ActorID, Type: findOperationResult.OpType, CreatedAt: findOperationResult.CreatedAt}
	existingPayload = findOperationResult.Payload

	if err == nil {
		// Reusing an idempotency key for different content is a client bug.
		if existing.ActorID != userID ||
			existing.Type != req.Type ||
			!matchingPayload(req.Type, existingPayload, req.Payload) {
			return SubmitResponse{}, opError(http.StatusConflict, "idempotency key already used for another operation")
		}

		existing.Payload = existingPayload

		return SubmitResponse{
			Operation:  existing,
			Duplicate:  true,
			CurrentSeq: currentSeq,
		}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return SubmitResponse{}, opError(http.StatusInternalServerError, "could not check operation history")
	}

	if req.BaseSeq > currentSeq {
		return SubmitResponse{}, opError(http.StatusConflict, "base_seq is ahead of the current project sequence")
	}

	if req.Type == "restore_board" && req.BaseSeq != currentSeq {
		return SubmitResponse{}, opError(409, "board changed since restore was prepared; refresh history and try again")
	}
	if err := board.EnsureBaseline(ctx, tx, projectID, userID, currentSeq); err != nil {
		return SubmitResponse{}, opError(500, "could not preserve board baseline")
	}
	if req.Type == "restore_board" {
		intent, err := parseRestoreIntent(req.Payload)
		if err != nil {
			return SubmitResponse{}, err
		}
		if *intent.Seq > currentSeq {
			return SubmitResponse{}, opError(400, "restore sequence is ahead of the project")
		}
		if s.restoreLoader == nil {
			return SubmitResponse{}, opError(503, "history restore is not configured")
		}
		state, err := s.restoreLoader(ctx, tx, projectID, *intent.Seq)
		if err != nil {
			return SubmitResponse{}, opError(409, "historical board could not be reconstructed from its baseline")
		}
		if err := board.Validate(state, projectID); err != nil {
			return SubmitResponse{}, opError(409, "historical board failed validation")
		}
		if err := board.Replace(ctx, tx, state); err != nil {
			return SubmitResponse{}, opError(409, "restored items conflict with current data")
		}
		req.Payload, err = json.Marshal(struct {
			Seq        int64       `json:"seq"`
			BoardState board.State `json:"board_state"`
		}{*intent.Seq, state})
		if err != nil {
			return SubmitResponse{}, opError(500, "could not encode restored board")
		}
	} else {
		// Apply the board mutation inside the same transaction as the log append.
		if err := applyOperation(ctx, tx, projectID, userID, req.Type, p); err != nil {
			var opErr *operationError
			if errors.As(err, &opErr) {
				return SubmitResponse{}, opError(opErr.status, opErr.message)
			}
			return SubmitResponse{}, opError(http.StatusInternalServerError, "could not apply operation")
		}

	}

	nextSeq, err := queries.AllocateProjectSequence(ctx, projectID)
	if err != nil {
		return SubmitResponse{}, opError(http.StatusInternalServerError, "could not allocate sequence")
	}

	op := Operation{
		ID:        uuid.New(),
		ProjectID: projectID,
		Seq:       nextSeq,
		ClientID:  req.ClientID,
		OpID:      req.OpID,
		ActorID:   userID,
		Type:      req.Type,
		Payload:   req.Payload,
	}

	appendOperationResult, err := queries.AppendOperation(ctx, db.AppendOperationParams{ID: op.ID, ProjectID: projectID, Seq: op.Seq, ClientID: op.ClientID, OpID: op.OpID, ActorID: op.ActorID, OpType: op.Type, Payload: op.Payload})
	op.CreatedAt = appendOperationResult
	if err != nil {
		return SubmitResponse{}, opError(http.StatusInternalServerError, "could not append operation")
	}
	if nextSeq%100 == 0 || req.Type == "restore_board" {
		if err := board.SaveSnapshot(ctx, tx, projectID, userID, nextSeq); err != nil {
			return SubmitResponse{}, opError(500, "could not save project snapshot")
		}
	}

	return SubmitResponse{
		Operation:  op,
		Duplicate:  false,
		CurrentSeq: nextSeq,
	}, nil
}
