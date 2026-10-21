package ops

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/google/uuid"
)

// Service is the single transaction and publication pipeline for all transports.
// Configure the publisher before serving requests.
type Service struct {
	database  *sql.DB
	publisher func(uuid.UUID, Operation)
}

func NewService(database *sql.DB) *Service                           { return &Service{database: database} }
func (s *Service) SetPublisher(publisher func(uuid.UUID, Operation)) { s.publisher = publisher }

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

	p, err := decodePayload(req.Payload)
	if err != nil {
		return SubmitResponse{}, opError(http.StatusBadRequest, "invalid operation payload")
	}

	tx, err := s.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return SubmitResponse{}, opError(http.StatusInternalServerError, "could not start operation")
	}
	defer tx.Rollback()

	// Lock this project row. All writers for this project serialize here.
	var currentSeq int64
	err = tx.QueryRowContext(ctx, `
		SELECT last_seq
		FROM projects
		WHERE id = $1
		  AND EXISTS (
		    SELECT 1
		    FROM project_members pm
		    JOIN workspace_members wm
		      ON wm.user_id = pm.user_id
		    WHERE pm.project_id = projects.id
		      AND pm.user_id = $2
		      AND wm.workspace_id = projects.workspace_id
		  )
		FOR UPDATE
	`, projectID, userID).Scan(&currentSeq)

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
	err = tx.QueryRowContext(ctx, `
		SELECT id, project_id, seq, client_id, op_id, actor_id,
		       op_type, payload, created_at
		FROM project_ops
		WHERE project_id = $1 AND client_id = $2 AND op_id = $3
	`, projectID, req.ClientID, req.OpID).Scan(
		&existing.ID, &existing.ProjectID, &existing.Seq,
		&existing.ClientID, &existing.OpID, &existing.ActorID,
		&existing.Type, &existingPayload, &existing.CreatedAt,
	)

	if err == nil {
		// Reusing an idempotency key for different content is a client bug.
		if existing.ActorID != userID ||
			existing.Type != req.Type ||
			!jsonEqual(existingPayload, req.Payload) {
			return SubmitResponse{}, opError(http.StatusConflict, "idempotency key already used for another operation")
		}

		existing.Payload = existingPayload
		if err := tx.Commit(); err != nil {
			return SubmitResponse{}, opError(http.StatusInternalServerError, "could not finish duplicate request")
		}

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

	// Apply the board mutation inside the same transaction as the log append.
	if err := applyOperation(ctx, tx, projectID, userID, req.Type, p); err != nil {
		var opErr *operationError
		if errors.As(err, &opErr) {
			return SubmitResponse{}, opError(opErr.status, opErr.message)
		}
		return SubmitResponse{}, opError(http.StatusInternalServerError, "could not apply operation")
	}

	var nextSeq int64
	err = tx.QueryRowContext(ctx, `
		UPDATE projects
		SET last_seq = last_seq + 1,
		    updated_at = NOW()
		WHERE id = $1
		RETURNING last_seq
	`, projectID).Scan(&nextSeq)
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

	err = tx.QueryRowContext(ctx, `
		INSERT INTO project_ops (
		    id, project_id, seq, client_id, op_id, actor_id, op_type, payload
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb)
		RETURNING created_at
	`, op.ID, projectID, op.Seq, op.ClientID, op.OpID,
		op.ActorID, op.Type, string(op.Payload),
	).Scan(&op.CreatedAt)
	if err != nil {
		return SubmitResponse{}, opError(http.StatusInternalServerError, "could not append operation")
	}

	if err := tx.Commit(); err != nil {
		return SubmitResponse{}, opError(http.StatusInternalServerError, "could not commit operation")
	}

	if s.publisher != nil {
		s.publisher(projectID, op)
	}

	return SubmitResponse{
		Operation:  op,
		Duplicate:  false,
		CurrentSeq: nextSeq,
	}, nil
}
