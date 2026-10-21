-- name: LockProjectForOperation :one
SELECT last_seq
		FROM projects
		WHERE id = sqlc.arg(project_id)
		  AND EXISTS (
		    SELECT 1
		    FROM project_members pm
		    JOIN workspace_members wm
		      ON wm.user_id = pm.user_id
		    WHERE pm.project_id = projects.id
		      AND pm.user_id = sqlc.arg(user_id)
		      AND wm.workspace_id = projects.workspace_id
		  )
		FOR UPDATE;

-- name: FindOperation :one
SELECT id, project_id, seq, client_id, op_id, actor_id,
		       op_type, payload, created_at
		FROM project_ops
		WHERE project_id = sqlc.arg(project_id) AND client_id = sqlc.arg(client_id) AND op_id = sqlc.arg(op_id);

-- name: AllocateProjectSequence :one
UPDATE projects
		SET last_seq = last_seq + 1,
		    updated_at = NOW()
		WHERE id = sqlc.arg(project_id)
		RETURNING last_seq;

-- name: AppendOperation :one
INSERT INTO project_ops (
		    id, project_id, seq, client_id, op_id, actor_id, op_type, payload
		)
		VALUES (sqlc.arg(id), sqlc.arg(project_id), sqlc.arg(seq), sqlc.arg(client_id), sqlc.arg(op_id), sqlc.arg(actor_i_d), sqlc.arg(op_type), sqlc.arg(payload)::jsonb)
		RETURNING created_at;

-- name: GetOperationRole :one
SELECT pm.role
		FROM project_members pm
		JOIN projects p ON p.id = pm.project_id
		JOIN workspace_members wm
		  ON wm.workspace_id = p.workspace_id
		 AND wm.user_id = pm.user_id
		WHERE pm.project_id = sqlc.arg(project_id)
		  AND pm.user_id = sqlc.arg(user_id);

-- name: SceneExists :one
SELECT EXISTS(
		    SELECT 1 FROM scenes
		    WHERE id = sqlc.arg(scene_id) AND project_id = sqlc.arg(project_id)
		);

-- name: ShotExists :one
SELECT EXISTS(
		    SELECT 1
		    FROM shots s
		    JOIN scenes sc ON sc.id = s.scene_id
		    WHERE s.id = sqlc.arg(shot_id)
		      AND s.scene_id = sqlc.arg(scene_id)
		      AND sc.project_id = sqlc.arg(project_id)
		);

-- name: CreateOperationScene :execrows
INSERT INTO scenes (
				id, project_id, title, description, position, created_by
			)
			VALUES (
				sqlc.arg(scene_id), sqlc.arg(project_id), sqlc.arg(title), sqlc.arg(description),
				COALESCE(
					(SELECT MAX(position) + 1 FROM scenes WHERE project_id = sqlc.arg(project_id)),
					0
				),
				sqlc.arg(user_id)
			);

-- name: UpdateOperationScene :execrows
UPDATE scenes
			SET title = COALESCE(sqlc.narg(title), title),
			    description = COALESCE(sqlc.narg(description), description),
			    updated_at = NOW()
			WHERE id = sqlc.arg(scene_id) AND project_id = sqlc.arg(project_id);

-- name: DeleteOperationScene :execrows
DELETE FROM scenes WHERE id = sqlc.arg(scene_id) AND project_id = sqlc.arg(project_id);

-- name: CreateOperationShot :execrows
INSERT INTO shots (
			    id, scene_id, prompt, style, notes, image_url, position, created_by
			)
			VALUES (
			    sqlc.arg(shot_id), sqlc.arg(scene_id), sqlc.arg(prompt), sqlc.arg(style), sqlc.arg(notes), sqlc.narg(image_url),
			    COALESCE((SELECT MAX(position) + 1 FROM shots WHERE scene_id = sqlc.arg(scene_id)), 0),
			    sqlc.arg(user_id)
			);

-- name: UpdateOperationShotWithImage :execrows
UPDATE shots AS sh
				SET prompt = COALESCE(sqlc.narg(prompt), prompt),
				    style = COALESCE(sqlc.narg(style), style),
				    notes = COALESCE(sqlc.narg(notes), notes),
				    image_url = sqlc.narg(image_url),
				    updated_at = NOW()
				WHERE sh.id = sqlc.arg(shot_id) AND sh.scene_id = sqlc.arg(scene_id)
				  AND EXISTS (
				    SELECT 1 FROM scenes sc WHERE sc.id = sqlc.arg(scene_id) AND sc.project_id = sqlc.arg(project_id)
				  );

-- name: UpdateOperationShot :execrows
UPDATE shots AS sh
				SET prompt = COALESCE(sqlc.narg(prompt), prompt),
				    style = COALESCE(sqlc.narg(style), style),
				    notes = COALESCE(sqlc.narg(notes), notes),
				    updated_at = NOW()
				WHERE sh.id = sqlc.arg(shot_id) AND sh.scene_id = sqlc.arg(scene_id)
				  AND EXISTS (
				    SELECT 1 FROM scenes sc WHERE sc.id = sqlc.arg(scene_id) AND sc.project_id = sqlc.arg(project_id)
				  );

-- name: DeleteOperationShot :execrows
DELETE FROM shots s
			USING scenes sc
			WHERE s.id = sqlc.arg(shot_id) AND s.scene_id = sqlc.arg(scene_id)
			  AND sc.id = s.scene_id AND sc.project_id = sqlc.arg(project_id);

-- name: ReorderOperationScene :execrows
UPDATE scenes SET position = sqlc.arg(position)::bigint, updated_at = NOW()
				WHERE id = sqlc.arg(id) AND project_id = sqlc.arg(project_id);

-- name: ReorderOperationShot :execrows
UPDATE shots s SET position = sqlc.arg(position)::bigint, updated_at = NOW()
				WHERE s.id = sqlc.arg(id) AND s.scene_id = sqlc.arg(scene_id)
				  AND EXISTS (
				    SELECT 1 FROM scenes sc WHERE sc.id = sqlc.arg(scene_id) AND sc.project_id = sqlc.arg(project_id)
				  );

-- name: GetReplaySequence :one
SELECT last_seq
		FROM projects
		WHERE id = sqlc.arg(project_id)
		  AND EXISTS (
		    SELECT 1
		    FROM project_members pm
		    JOIN workspace_members wm
		      ON wm.workspace_id = projects.workspace_id
		     AND wm.user_id = pm.user_id
		    WHERE pm.project_id = projects.id
		      AND pm.user_id = sqlc.arg(user_id)
		  );

-- name: ListReplayOperations :many
SELECT id, project_id, seq, client_id, op_id, actor_id,
		       op_type, payload, created_at
		FROM project_ops
		WHERE project_id = sqlc.arg(project_id) AND seq > sqlc.arg(after_seq)
		ORDER BY seq ASC
		LIMIT sqlc.arg(row_limit);
