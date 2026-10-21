package websocket

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
)

type Store struct {
	DB *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{DB: db}
}

func (s *Store) Role(
	ctx context.Context,
	projectID, userID uuid.UUID,
) (string, error) {
	var role string
	err := s.DB.QueryRowContext(ctx, `
		SELECT pm.role
		FROM project_members pm
		JOIN projects p ON p.id = pm.project_id
		JOIN workspace_members wm
		  ON wm.workspace_id = p.workspace_id
		 AND wm.user_id = pm.user_id
		WHERE pm.project_id = $1 AND pm.user_id = $2
	`, projectID, userID).Scan(&role)
	return role, err
}

func (s *Store) Replay(
	ctx context.Context,
	projectID, userID uuid.UUID,
	afterSeq int64,
	limit int,
) ([]json.RawMessage, int64, bool, error) {
	var currentSeq int64

	err := s.DB.QueryRowContext(ctx, `
		SELECT p.last_seq
		FROM projects p
		JOIN project_members pm ON pm.project_id = p.id
		JOIN workspace_members wm
		  ON wm.workspace_id = p.workspace_id
		 AND wm.user_id = pm.user_id
		WHERE p.id = $1 AND pm.user_id = $2
	`, projectID, userID).Scan(&currentSeq)
	if err != nil {
		return nil, 0, false, err
	}

	rows, err := s.DB.QueryContext(ctx, `
		SELECT jsonb_build_object(
			'id', id,
			'project_id', project_id,
			'seq', seq,
			'client_id', client_id,
			'op_id', op_id,
			'actor_id', actor_id,
			'type', op_type,
			'payload', payload,
			'created_at', created_at
		)
		FROM project_ops
		WHERE project_id = $1 AND seq > $2
		ORDER BY seq ASC
		LIMIT $3
	`, projectID, afterSeq, limit+1)
	if err != nil {
		return nil, 0, false, err
	}
	defer rows.Close()

	operations := make([]json.RawMessage, 0, limit)
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, 0, false, err
		}
		operations = append(operations, json.RawMessage(raw))
	}
	if err := rows.Err(); err != nil {
		return nil, 0, false, err
	}

	hasMore := len(operations) > limit
	if hasMore {
		operations = operations[:limit]
	}
	return operations, currentSeq, hasMore, nil
}

func (s *Store) CanWrite(
	ctx context.Context,
	projectID, userID uuid.UUID,
) (bool, error) {
	role, err := s.Role(ctx, projectID, userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, sql.ErrNoRows
		}
		return false, err
	}
	return role == "owner" || role == "editor", nil
}

func authFailureStatus(err error) int {
	if errors.Is(err, sql.ErrNoRows) {
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}
