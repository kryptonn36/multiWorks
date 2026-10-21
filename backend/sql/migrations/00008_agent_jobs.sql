-- +goose Up
ALTER TABLE agent_proposals DROP CONSTRAINT agent_proposals_status_check;
ALTER TABLE agent_proposals ADD CONSTRAINT agent_proposals_status_check
    CHECK (status IN ('generating','pending','approved','rejected','stale','failed'));

CREATE TABLE agent_jobs (
    proposal_id UUID PRIMARY KEY REFERENCES agent_proposals(id) ON DELETE CASCADE,
    board_context JSONB NOT NULL CHECK (octet_length(board_context::text) <= 262144),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','processing','completed')),
    attempts INT NOT NULL DEFAULT 0,
    locked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX agent_jobs_pending_idx ON agent_jobs(status,created_at);

-- +goose Down
DROP TABLE agent_jobs;
UPDATE agent_proposals SET status='failed',failure_reason='Agent generation stopped by migration rollback.' WHERE status='generating';
ALTER TABLE agent_proposals DROP CONSTRAINT agent_proposals_status_check;
ALTER TABLE agent_proposals ADD CONSTRAINT agent_proposals_status_check
    CHECK (status IN ('pending','approved','rejected','stale','failed'));
