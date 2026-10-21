-- name: LockProject :one
SELECT id FROM projects WHERE id=sqlc.arg(project_id) FOR UPDATE;

-- name: LockWorkspace :one
SELECT id FROM workspaces WHERE id=sqlc.arg(workspace_id) FOR UPDATE;

-- name: LockWorkspaceProjects :many
SELECT id FROM projects WHERE workspace_id=sqlc.arg(workspace_id) ORDER BY id FOR UPDATE;

-- name: ListWorkspaceProjectIDs :many
SELECT id FROM projects WHERE workspace_id=sqlc.arg(workspace_id);
