-- +goose Up
CREATE TABLE assets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    sha256 CHAR(64) NOT NULL,
    mime_type TEXT NOT NULL,
    size_bytes BIGINT NOT NULL CHECK (size_bytes > 0),
    storage_key TEXT NOT NULL,
    thumbnail_key TEXT,
    status TEXT NOT NULL DEFAULT 'processing' CHECK (status IN ('processing','ready','failed')),
    error_message TEXT,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(project_id,sha256),
    UNIQUE(project_id,id)
);
CREATE TABLE shot_assets (
    project_id UUID NOT NULL,
    shot_id UUID NOT NULL REFERENCES shots(id) ON DELETE CASCADE,
    asset_id UUID NOT NULL,
    attached_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(shot_id,asset_id),
    FOREIGN KEY(project_id,asset_id) REFERENCES assets(project_id,id) ON DELETE CASCADE
);
CREATE INDEX shot_assets_asset_idx ON shot_assets(asset_id);
CREATE INDEX assets_project_created_idx ON assets(project_id,created_at DESC);
CREATE TABLE asset_jobs (
    id BIGSERIAL PRIMARY KEY,
    asset_id UUID NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    job_type TEXT NOT NULL CHECK(job_type='thumbnail'),
    status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','processing','completed','failed')),
    attempts INT NOT NULL DEFAULT 0,
    available_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    locked_at TIMESTAMPTZ,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(asset_id,job_type)
);
CREATE INDEX asset_jobs_pending_idx ON asset_jobs(status,available_at,id);
CREATE TABLE agent_proposals (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    requested_by UUID REFERENCES users(id) ON DELETE SET NULL,
    instruction TEXT NOT NULL,
    generation_mode TEXT NOT NULL DEFAULT 'normal',
    base_seq BIGINT NOT NULL CHECK(base_seq>=0),
    status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','approved','rejected','stale','failed')),
    proposed_ops JSONB NOT NULL DEFAULT '[]'::jsonb,
    applied_ops JSONB NOT NULL DEFAULT '[]'::jsonb,
    failure_reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX agent_proposals_project_created_idx ON agent_proposals(project_id,created_at DESC);
-- +goose Down
DROP TABLE agent_proposals;
DROP TABLE asset_jobs;
DROP TABLE shot_assets;
DROP TABLE assets;
