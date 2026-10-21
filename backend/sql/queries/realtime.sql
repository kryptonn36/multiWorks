-- name: GetSocketRole :one
SELECT pm.role
		FROM project_members pm
		JOIN projects p ON p.id = pm.project_id
		JOIN workspace_members wm
		  ON wm.workspace_id = p.workspace_id
		 AND wm.user_id = pm.user_id
		WHERE pm.project_id = sqlc.arg(project_id) AND pm.user_id = sqlc.arg(user_id);

-- name: GetSocketSequence :one
SELECT p.last_seq
		FROM projects p
		JOIN project_members pm ON pm.project_id = p.id
		JOIN workspace_members wm
		  ON wm.workspace_id = p.workspace_id
		 AND wm.user_id = pm.user_id
		WHERE p.id = sqlc.arg(project_id) AND pm.user_id = sqlc.arg(user_id);

-- name: ListSocketReplay :many
SELECT jsonb_build_object(
			'id', id,
			'project_id', project_id,
			'seq', seq,
			'client_id', client_id,
			'op_id', op_id,
			'actor_id', actor_id,
			'type', op_type,
			'payload', payload,
			'created_at', created_at
		)
		FROM project_ops
		WHERE project_id = sqlc.arg(project_id) AND seq > sqlc.arg(after_seq) AND (sqlc.narg(boundary_seq)::bigint IS NULL OR seq <= sqlc.narg(boundary_seq))
		ORDER BY seq ASC
		LIMIT sqlc.arg(row_limit);

-- name: DeleteExpiredSocketTickets :execrows
DELETE FROM websocket_tickets WHERE expires_at <= NOW();

-- name: CreateSocketTicket :execrows
INSERT INTO websocket_tickets (ticket_hash, project_id, user_id, expires_at) VALUES (sqlc.arg(ticket_hash),sqlc.arg(project_id),sqlc.arg(user_id),NOW() + INTERVAL '30 seconds');

-- name: ConsumeSocketTicket :one
DELETE FROM websocket_tickets WHERE ticket_hash=sqlc.arg(ticket_hash) AND project_id=sqlc.arg(project_id) AND expires_at>NOW() RETURNING user_id;
