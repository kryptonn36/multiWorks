-- +goose Up

CREATE TABLE workspaces (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL
        CHECK (char_length(trim(name)) BETWEEN 1 AND 100),
    created_by UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE workspace_members (
    workspace_id UUID NOT NULL
        REFERENCES workspaces(id) ON DELETE CASCADE,
    user_id UUID NOT NULL
        REFERENCES users(id) ON DELETE CASCADE,
    role TEXT NOT NULL
        CHECK (role IN ('owner', 'admin', 'member')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (workspace_id, user_id)
);

CREATE INDEX workspace_members_user_idx
    ON workspace_members(user_id, workspace_id);

CREATE TABLE projects (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL
        REFERENCES workspaces(id) ON DELETE CASCADE,
    name TEXT NOT NULL
        CHECK (char_length(trim(name)) BETWEEN 1 AND 100),
    created_by UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    last_seq BIGINT NOT NULL DEFAULT 0 CHECK (last_seq >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX projects_workspace_idx
    ON projects(workspace_id, created_at DESC);

CREATE TABLE project_members (
    project_id UUID NOT NULL
        REFERENCES projects(id) ON DELETE CASCADE,
    user_id UUID NOT NULL
        REFERENCES users(id) ON DELETE CASCADE,
    role TEXT NOT NULL
        CHECK (role IN ('owner', 'editor', 'viewer')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (project_id, user_id)
);

CREATE INDEX project_members_user_idx
    ON project_members(user_id, project_id);

CREATE UNIQUE INDEX project_single_owner_idx
    ON project_members(project_id)
    WHERE role = 'owner';

-- +goose Down

DROP TABLE IF EXISTS project_members;
DROP TABLE IF EXISTS projects;
DROP TABLE IF EXISTS workspace_members;
DROP TABLE IF EXISTS workspaces;
