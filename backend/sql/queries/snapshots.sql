-- name: ListSnapshots :many
SELECT id,seq,created_by,created_at::text FROM project_snapshots WHERE project_id=sqlc.arg(project_id) AND seq<sqlc.arg(before_seq) ORDER BY seq DESC LIMIT 100;

-- name: FindSnapshotAtSequence :one
SELECT seq,board_state FROM project_snapshots WHERE project_id=sqlc.arg(project_id) AND seq<=sqlc.arg(target_seq) ORDER BY seq DESC LIMIT 1;

-- name: FindFirstSnapshot :one
SELECT seq,board_state FROM project_snapshots WHERE project_id=sqlc.arg(project_id) ORDER BY seq LIMIT 1;

-- name: CreateSnapshot :execrows
INSERT INTO project_snapshots(project_id,seq,board_state,created_by) VALUES(sqlc.arg(project_id),sqlc.arg(seq),sqlc.arg(payload)::jsonb,sqlc.arg(user_id)) ON CONFLICT(project_id,seq) DO NOTHING;

-- name: HasSnapshot :one
SELECT EXISTS(SELECT 1 FROM project_snapshots WHERE project_id=sqlc.arg(project_id));
