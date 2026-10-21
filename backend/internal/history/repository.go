package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/kryptonn36/multiWorks/internal/db"

	"github.com/google/uuid"
	"github.com/kryptonn36/multiWorks/internal/board"
)

var ErrUnavailable = errors.New("history predates a reliable baseline or contains a gap")

type Repository struct{ database *sql.DB }

func NewRepository(database *sql.DB) *Repository { return &Repository{database: database} }

type Snapshot struct {
	ID        uuid.UUID  `json:"id"`
	Seq       int64      `json:"seq"`
	CreatedBy *uuid.UUID `json:"created_by"`
	CreatedAt string     `json:"created_at"`
}
type OperationMetadata struct {
	Seq       int64     `json:"seq"`
	Type      string    `json:"type"`
	ActorID   uuid.UUID `json:"actor_id"`
	CreatedAt string    `json:"created_at"`
}

func (r *Repository) Access(ctx context.Context, tx *sql.Tx, project, user uuid.UUID) (int64, string, error) {
	getHistoryAccessResult, err := db.New(tx).GetHistoryAccess(ctx, db.GetHistoryAccessParams{ProjectID: project, UserID: user})
	return getHistoryAccessResult.LastSeq, getHistoryAccessResult.Role, err
}
func (r *Repository) List(ctx context.Context, tx *sql.Tx, project uuid.UUID, before int64) ([]Snapshot, []OperationMetadata, error) {
	queries := db.New(tx)
	snapshotRows, err := queries.ListSnapshots(ctx, db.ListSnapshotsParams{ProjectID: project, BeforeSeq: before})
	if err != nil {
		return nil, nil, err
	}
	snapshots := make([]Snapshot, 0, len(snapshotRows))
	for _, row := range snapshotRows {
		var creator *uuid.UUID
		if row.CreatedBy.Valid {
			id := row.CreatedBy.UUID
			creator = &id
		}
		snapshots = append(snapshots, Snapshot{ID: row.ID, Seq: row.Seq, CreatedBy: creator, CreatedAt: row.CreatedAt})
	}
	operationRows, err := queries.ListOperationMetadata(ctx, db.ListOperationMetadataParams{ProjectID: project, BeforeSeq: before})
	if err != nil {
		return nil, nil, err
	}
	operations := make([]OperationMetadata, 0, len(operationRows))
	for _, row := range operationRows {
		operations = append(operations, OperationMetadata{Seq: row.Seq, Type: row.OpType, ActorID: row.ActorID, CreatedAt: row.CreatedAt})
	}
	return snapshots, operations, nil
}

func (r *Repository) Reconstruct(ctx context.Context, tx *sql.Tx, project uuid.UUID, target int64) (board.State, error) {
	state := board.State{ProjectID: project, Scenes: []board.Scene{}}
	var raw []byte
	var checkpointSeq int64
	findSnapshotAtSequenceResult, err := db.New(tx).FindSnapshotAtSequence(ctx, db.FindSnapshotAtSequenceParams{ProjectID: project, TargetSeq: target})
	checkpointSeq = findSnapshotAtSequenceResult.Seq
	raw = findSnapshotAtSequenceResult.BoardState
	var verify *board.State
	through := target
	if errors.Is(err, sql.ErrNoRows) {
		// A migrated project may have legacy rows predating project_ops. Only trust
		// empty-state reconstruction if it also reproduces the first known checkpoint.
		findFirstSnapshotResult, err := db.New(tx).FindFirstSnapshot(ctx, project)
		checkpointSeq = findFirstSnapshotResult.Seq
		raw = findFirstSnapshotResult.BoardState
		if err == nil {
			var known board.State
			if json.Unmarshal(raw, &known) != nil || known.LastSeq != checkpointSeq || board.Validate(known, project) != nil {
				return state, ErrUnavailable
			}
			known = board.Canonical(known)
			verify = &known
			through = checkpointSeq
		} else if !errors.Is(err, sql.ErrNoRows) {
			return state, err
		}
	} else if err != nil {
		return state, err
	} else {
		if json.Unmarshal(raw, &state) != nil || state.LastSeq != checkpointSeq || board.Validate(state, project) != nil {
			return state, ErrUnavailable
		}
	}
	state = board.Canonical(state)
	result := board.Clone(state)
	for state.LastSeq < through {
		rows, err := db.New(tx).ListHistoricalOperations(ctx, db.ListHistoricalOperationsParams{ProjectID: project, AfterSeq: state.LastSeq, ThroughSeq: through})
		if err != nil {
			return state, err
		}
		operations := make([]board.Operation, 0, len(rows))
		for _, row := range rows {
			operations = append(operations, board.Operation{ProjectID: project, Seq: row.Seq, Type: row.OpType, Payload: row.Payload, ActorID: row.ActorID, CreatedAt: row.CreatedAt})
		}

		if len(operations) == 0 {
			return state, ErrUnavailable
		}
		for _, op := range operations {
			next, err := board.Apply(state, op)
			if err != nil {
				return state, fmt.Errorf("%w: %v", ErrUnavailable, err)
			}
			state = next
			if state.LastSeq == target {
				result = board.Clone(state)
			}
		}
	}
	if verify != nil {
		// JSON normalizes time zones and avoids time.Time location-pointer differences.
		left, _ := json.Marshal(state)
		right, _ := json.Marshal(verify)
		var a, b any
		json.Unmarshal(left, &a)
		json.Unmarshal(right, &b)
		canonicalA, _ := json.Marshal(a)
		canonicalB, _ := json.Marshal(b)
		if string(canonicalA) != string(canonicalB) {
			return result, ErrUnavailable
		}
	}
	if target == state.LastSeq {
		result = state
	}
	if result.LastSeq != target {
		return result, ErrUnavailable
	}
	return result, nil
}
