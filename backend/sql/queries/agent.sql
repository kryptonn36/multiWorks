-- name: CreateAgentProposal :one
INSERT INTO agent_proposals(project_id,requested_by,instruction,generation_mode,base_seq,status,proposed_ops,failure_reason)
VALUES(sqlc.arg(project_id),sqlc.narg(requested_by),sqlc.arg(instruction),sqlc.arg(generation_mode),sqlc.arg(base_seq),sqlc.arg(status),sqlc.arg(proposed_ops),sqlc.narg(failure_reason)) RETURNING *;

-- name: ListAgentProposals :many
SELECT * FROM agent_proposals WHERE project_id=sqlc.arg(project_id) AND id>sqlc.arg(after_id) ORDER BY id LIMIT 101;

-- name: GetAgentProposal :one
SELECT * FROM agent_proposals WHERE project_id=sqlc.arg(project_id) AND id=sqlc.arg(id);

-- name: LockAgentProposal :one
SELECT * FROM agent_proposals WHERE project_id=sqlc.arg(project_id) AND id=sqlc.arg(id) FOR UPDATE;

-- name: DecideAgentProposal :one
UPDATE agent_proposals SET status=sqlc.arg(status),applied_ops=sqlc.arg(applied_ops),failure_reason=sqlc.narg(failure_reason),updated_at=NOW()
WHERE project_id=sqlc.arg(project_id) AND id=sqlc.arg(id) RETURNING *;

-- name: AgentContextBudget :one
-- A conservative bound checked before materializing board JSON in Go.
SELECT (1024
 + (SELECT COALESCE(SUM(1000 + 6*(octet_length(sc.title)+octet_length(sc.description))),0) FROM scenes sc WHERE sc.project_id=sqlc.arg(project_id))
 + (SELECT COALESCE(SUM(1000 + 6*(octet_length(sh.prompt)+octet_length(sh.style)+octet_length(sh.notes)+octet_length(COALESCE(sh.image_url,'')))),0) FROM shots sh JOIN scenes sc ON sc.id=sh.scene_id WHERE sc.project_id=sqlc.arg(project_id))
 + (SELECT COUNT(*)*50 FROM shot_assets WHERE project_id=sqlc.arg(project_id)))::bigint AS budget;

-- name: EnqueueAgentJob :exec
INSERT INTO agent_jobs(proposal_id,board_context) VALUES(sqlc.arg(proposal_id),sqlc.arg(board_context));

-- name: ClaimAgentJob :one
WITH candidate AS (
 SELECT proposal_id FROM agent_jobs
 WHERE status='pending' OR (status='processing' AND locked_at<NOW()-INTERVAL '30 seconds')
 ORDER BY created_at,proposal_id FOR UPDATE SKIP LOCKED LIMIT 1
)
UPDATE agent_jobs j SET status='processing',attempts=j.attempts+1,locked_at=NOW(),updated_at=NOW()
FROM candidate WHERE j.proposal_id=candidate.proposal_id RETURNING j.*;

-- name: GetAgentJobProposal :one
SELECT * FROM agent_proposals WHERE id=sqlc.arg(id);

-- name: LockOwnedAgentJob :one
SELECT * FROM agent_jobs WHERE proposal_id=sqlc.arg(proposal_id) AND attempts=sqlc.arg(attempts) AND status='processing' FOR UPDATE;

-- name: CompleteAgentJob :exec
UPDATE agent_jobs SET status='completed',updated_at=NOW(),board_context='{}'::jsonb WHERE proposal_id=sqlc.arg(proposal_id);

-- name: FinishAgentProposal :one
UPDATE agent_proposals SET status=sqlc.arg(status),proposed_ops=sqlc.arg(proposed_ops),failure_reason=sqlc.narg(failure_reason),updated_at=NOW()
WHERE id=sqlc.arg(id) AND status='generating' RETURNING *;

-- name: AgentSceneIDExists :one
SELECT EXISTS(SELECT 1 FROM scenes WHERE id=sqlc.arg(id));

-- name: AgentShotIDExists :one
SELECT EXISTS(SELECT 1 FROM shots WHERE id=sqlc.arg(id));
