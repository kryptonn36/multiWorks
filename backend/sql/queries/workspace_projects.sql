
-- name: CreateWorkspace :one
INSERT INTO workspaces (id, name, created_by)
VALUES ($1, $2, $3)
RETURNING id, name, created_by, created_at;

-- name: AddWorkspaceMember :exec
INSERT INTO workspace_members (workspace_id, user_id, role)
VALUES ($1, $2, $3)
ON CONFLICT (workspace_id, user_id)
DO UPDATE SET role = CASE
    WHEN workspace_members.role = 'owner' THEN 'owner'
    ELSE EXCLUDED.role
END;

-- name: GetWorkspaceRole :one
SELECT role
FROM workspace_members
WHERE workspace_id = $1
  AND user_id = $2;

-- name: ListWorkspaces :many
SELECT w.id, w.name, w.created_by, w.created_at
FROM workspaces w
JOIN workspace_members wm ON wm.workspace_id = w.id
WHERE wm.user_id = $1
ORDER BY w.created_at DESC;

-- name: GetWorkspaceByID :one
SELECT w.id, w.name, w.created_by, w.created_at
FROM workspaces w
JOIN workspace_members wm ON wm.workspace_id = w.id
WHERE w.id = $1
  AND wm.user_id = $2;

-- name: ListWorkspaceMembers :many
SELECT
    u.id AS user_id,
    u.email,
    wm.role,
    wm.created_at AS joined_at
FROM workspace_members wm
JOIN users u ON u.id = wm.user_id
WHERE wm.workspace_id = $1
  AND EXISTS (
      SELECT 1
      FROM workspace_members viewer
      WHERE viewer.workspace_id = wm.workspace_id
        AND viewer.user_id = $2
  )
ORDER BY wm.created_at ASC;

-- name: CreateProject :one
INSERT INTO projects (id, workspace_id, name, created_by)
VALUES ($1, $2, $3, $4)
RETURNING id, workspace_id, name, created_by,
          last_seq, created_at, updated_at;

-- name: ListProjects :many
SELECT
    p.id,
    p.workspace_id,
    p.name,
    p.created_by,
    p.last_seq,
    p.created_at,
    p.updated_at,
    pm.role AS member_role
FROM projects p
JOIN workspace_members wm ON wm.workspace_id = p.workspace_id
JOIN project_members pm ON pm.project_id = p.id
WHERE p.workspace_id = $1
  AND wm.user_id = $2
  AND pm.user_id = $2
ORDER BY p.created_at DESC;

-- name: GetProjectByID :one
SELECT
    p.id,
    p.workspace_id,
    p.name,
    p.created_by,
    p.last_seq,
    p.created_at,
    p.updated_at,
    pm.role AS member_role
FROM projects p
JOIN workspace_members wm ON wm.workspace_id = p.workspace_id
JOIN project_members pm ON pm.project_id = p.id
WHERE p.id = $1
  AND wm.user_id = $2
  AND pm.user_id = $2;

-- name: GetProjectRole :one
SELECT pm.role
FROM project_members pm
JOIN projects p ON p.id = pm.project_id
JOIN workspace_members wm ON wm.workspace_id = p.workspace_id
WHERE pm.project_id = $1
  AND pm.user_id = $2
  AND wm.user_id = $2;

-- name: AddProjectMember :one
INSERT INTO project_members (project_id, user_id, role)
SELECT $1, $2, $3
WHERE EXISTS (
    SELECT 1
    FROM projects p
    JOIN workspace_members wm ON wm.workspace_id = p.workspace_id
    WHERE p.id = $1
      AND wm.user_id = $2
)
ON CONFLICT (project_id, user_id)
DO UPDATE SET role = CASE
    WHEN project_members.role = 'owner' THEN 'owner'
    ELSE EXCLUDED.role
END
RETURNING user_id;

-- name: ListProjectMembers :many
SELECT
    u.id AS user_id,
    u.email,
    pm.role,
    pm.created_at AS joined_at
FROM project_members pm
JOIN users u ON u.id = pm.user_id
JOIN projects p ON p.id = pm.project_id
JOIN workspace_members target_wm
    ON target_wm.workspace_id = p.workspace_id
   AND target_wm.user_id = pm.user_id
WHERE pm.project_id = $1
  AND EXISTS (
      SELECT 1
      FROM project_members viewer
      WHERE viewer.project_id = pm.project_id
        AND viewer.user_id = $2
  )
  AND EXISTS (
      SELECT 1
      FROM workspace_members viewer_wm
      WHERE viewer_wm.workspace_id = p.workspace_id
        AND viewer_wm.user_id = $2
  )
ORDER BY pm.created_at ASC;

-- name: GetUserByEmailForWorkspace :one
SELECT id, email
FROM users
WHERE LOWER(email) = LOWER($1);