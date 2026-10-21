-- name: GetHistoryAccess :one
SELECT p.last_seq,pm.role FROM projects p JOIN project_members pm ON pm.project_id=p.id JOIN workspace_members wm ON wm.workspace_id=p.workspace_id AND wm.user_id=pm.user_id WHERE p.id=sqlc.arg(project_id) AND pm.user_id=sqlc.arg(user_id);

-- name: ListOperationMetadata :many
SELECT seq,op_type,actor_id,created_at::text FROM project_ops WHERE project_id=sqlc.arg(project_id) AND seq<sqlc.arg(before_seq) ORDER BY seq DESC LIMIT 100;

-- name: ListHistoricalOperations :many
SELECT seq,op_type,payload,actor_id,created_at FROM project_ops WHERE project_id=sqlc.arg(project_id) AND seq>sqlc.arg(after_seq) AND seq<=sqlc.arg(through_seq) ORDER BY seq LIMIT 500;
