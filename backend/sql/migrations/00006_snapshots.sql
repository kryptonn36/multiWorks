-- +goose Up

CREATE TABLE IF NOT EXISTS project_snapshots (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    seq BIGINT NOT NULL CHECK (seq >= 0),
    board_state JSONB NOT NULL,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT project_snapshots_project_seq_unique
        UNIQUE (project_id, seq)
);

CREATE INDEX IF NOT EXISTS idx_project_snapshots_project_seq
    ON project_snapshots (project_id, seq DESC);


-- Preserve existing board data at its actual committed sequence on upgrade.
INSERT INTO project_snapshots(project_id,seq,board_state,created_by)
SELECT p.id,p.last_seq,jsonb_build_object('project_id',p.id,'last_seq',p.last_seq,'scenes',COALESCE((SELECT jsonb_agg(to_jsonb(sc) || jsonb_build_object('shots',COALESCE((SELECT jsonb_agg(to_jsonb(sh) ORDER BY sh.position,sh.created_at,sh.id) FROM shots sh WHERE sh.scene_id=sc.id),'[]'::jsonb)) ORDER BY sc.position,sc.created_at,sc.id) FROM scenes sc WHERE sc.project_id=p.id),'[]'::jsonb)),p.created_by FROM projects p
ON CONFLICT(project_id,seq) DO NOTHING;

-- +goose Down

DROP TABLE IF EXISTS project_snapshots;
