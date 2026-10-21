-- +goose Up

CREATE TABLE project_ops (
    id UUID PRIMARY KEY,
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    seq BIGINT NOT NULL CHECK (seq > 0),
    client_id TEXT NOT NULL CHECK (char_length(client_id) BETWEEN 1 AND 100),
    op_id TEXT NOT NULL CHECK (char_length(op_id) BETWEEN 1 AND 150),
    actor_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    op_type TEXT NOT NULL,
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT project_ops_project_seq_unique
        UNIQUE (project_id, seq),

    CONSTRAINT project_ops_idempotency_unique
        UNIQUE (project_id, client_id, op_id)
);

CREATE INDEX project_ops_replay_idx
    ON project_ops(project_id, seq);

CREATE INDEX project_ops_actor_idx
    ON project_ops(project_id, actor_id, created_at DESC);

-- +goose Down

DROP TABLE IF EXISTS project_ops;
