package websocket

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/kryptonn36/multiWorks/internal/db"
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
	return db.New(s.DB).GetSocketRole(ctx, db.GetSocketRoleParams{ProjectID: projectID, UserID: userID})
}

func (s *Store) Replay(
	ctx context.Context,
	projectID, userID uuid.UUID,
	afterSeq int64,
	limit int,
) ([]json.RawMessage, int64, bool, error) {
	return s.replay(ctx, projectID, userID, afterSeq, limit, nil)
}

func (s *Store) ReplayThrough(ctx context.Context, projectID, userID uuid.UUID, afterSeq int64, limit int, boundary int64) ([]json.RawMessage, int64, bool, error) {
	return s.replay(ctx, projectID, userID, afterSeq, limit, &boundary)
}

func (s *Store) replay(ctx context.Context, projectID, userID uuid.UUID, afterSeq int64, limit int, boundary *int64) ([]json.RawMessage, int64, bool, error) {
	currentSeq, err := db.New(s.DB).GetSocketSequence(ctx, db.GetSocketSequenceParams{ProjectID: projectID, UserID: userID})
	if err != nil {
		return nil, 0, false, err
	}

	rows, err := db.New(s.DB).ListSocketReplay(ctx, db.ListSocketReplayParams{ProjectID: projectID, AfterSeq: afterSeq, BoundarySeq: db.NullableInt64(boundary), RowLimit: int32(limit + 1)})
	if err != nil {
		return nil, 0, false, err
	}
	operations := rows
	if operations == nil {
		operations = []json.RawMessage{}
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

func (s *Store) Boundary(ctx context.Context, projectID, userID uuid.UUID) (int64, error) {
	return db.New(s.DB).GetSocketSequence(ctx, db.GetSocketSequenceParams{ProjectID: projectID, UserID: userID})
}
