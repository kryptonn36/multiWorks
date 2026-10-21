-- name: GetStoryboardRole :one
SELECT pm.role
		FROM project_members pm
		JOIN projects p ON p.id = pm.project_id
		JOIN workspace_members wm
		  ON wm.workspace_id = p.workspace_id
		 AND wm.user_id = pm.user_id
		WHERE p.id = sqlc.arg(project_id)
		  AND pm.user_id = sqlc.arg(user_id);

-- name: GetBoardSequence :one
SELECT last_seq FROM projects WHERE id = sqlc.arg(project_id);

-- name: ListBoardScenes :many
SELECT s.id, s.project_id, s.title, s.description, s.position,
			s.created_by, s.created_at, s.updated_at
		FROM scenes s
		WHERE s.project_id = sqlc.arg(project_id)
		AND EXISTS (
			SELECT 1
			FROM project_members pm
			JOIN projects p ON p.id = pm.project_id
			JOIN workspace_members wm
			ON wm.workspace_id = p.workspace_id
			AND wm.user_id = pm.user_id
			WHERE pm.project_id = sqlc.arg(project_id)
			AND pm.user_id = sqlc.arg(user_id)
			AND wm.user_id = sqlc.arg(user_id)
		)
		ORDER BY s.position, s.created_at, s.id;

-- name: ListBoardShots :many
SELECT s.id, s.scene_id, s.prompt, s.style, s.notes,
			s.image_url, s.position, s.created_by,
			s.created_at, s.updated_at,
 COALESCE((SELECT jsonb_agg(sa.asset_id ORDER BY sa.created_at,sa.asset_id) FROM shot_assets sa WHERE sa.shot_id=s.id),'[]'::jsonb)::jsonb AS asset_ids
		FROM shots s
		JOIN scenes sc ON sc.id = s.scene_id
		WHERE sc.project_id = sqlc.arg(project_id)
		AND EXISTS (
			SELECT 1
			FROM project_members pm
			JOIN projects p ON p.id = pm.project_id
			JOIN workspace_members wm
			ON wm.workspace_id = p.workspace_id
			AND wm.user_id = pm.user_id
			WHERE pm.project_id = sqlc.arg(project_id)
			AND pm.user_id = sqlc.arg(user_id)
			AND wm.user_id = sqlc.arg(user_id)
		)
		ORDER BY s.scene_id, s.position, s.created_at, s.id;

-- name: CreateStoryboardScene :one
INSERT INTO scenes (
		    id, project_id, title, description, position, created_by
		)
		SELECT sqlc.arg(id), sqlc.arg(project_id), sqlc.arg(title), sqlc.arg(description),
		       COALESCE((SELECT MAX(position) + 1
		                 FROM scenes WHERE project_id = sqlc.arg(project_id)), 0),
		       sqlc.arg(user_id)
		WHERE EXISTS (
		    SELECT 1
		    FROM project_members pm
		    JOIN projects p ON p.id = pm.project_id
		    JOIN workspace_members wm
		      ON wm.workspace_id = p.workspace_id
		     AND wm.user_id = pm.user_id
		    WHERE pm.project_id = sqlc.arg(project_id)
		      AND pm.user_id = sqlc.arg(user_id)
		      AND pm.role IN ('owner', 'editor')
		)
		RETURNING id, project_id, title, description, position,
		          created_by, created_at, updated_at;

-- name: UpdateStoryboardScene :one
UPDATE scenes s
		SET title=COALESCE(sqlc.narg(title),title),description=COALESCE(sqlc.narg(description),description),position=COALESCE(sqlc.narg(position)::bigint,position),updated_at=NOW()
		WHERE s.id = sqlc.arg(scene_id)
		AND s.project_id = sqlc.arg(project_id)
		AND EXISTS (
			SELECT 1
			FROM project_members pm
			JOIN projects p ON p.id = pm.project_id
			JOIN workspace_members wm
			ON wm.workspace_id = p.workspace_id
			AND wm.user_id = pm.user_id
			WHERE pm.project_id = sqlc.arg(project_id)
			AND pm.user_id = sqlc.arg(user_id)
			AND pm.role IN ('owner', 'editor')
		)
		RETURNING s.id, s.project_id, s.title, s.description, s.position,
		          s.created_by, s.created_at, s.updated_at;

-- name: DeleteStoryboardScene :execrows
DELETE FROM scenes s
		WHERE s.id = sqlc.arg(scene_id) AND s.project_id = sqlc.arg(project_id)
		  AND EXISTS (
		    SELECT 1 FROM project_members pm
		    JOIN projects p ON p.id = pm.project_id
		    JOIN workspace_members wm
		      ON wm.workspace_id = p.workspace_id
		     AND wm.user_id = pm.user_id
		    WHERE pm.project_id = sqlc.arg(project_id) AND pm.user_id = sqlc.arg(user_id)
		      AND pm.role IN ('owner', 'editor')
		  );

-- name: CreateStoryboardShot :one
INSERT INTO shots (
		    id, scene_id, prompt, style, notes, image_url, position, created_by
		)
		SELECT sqlc.arg(id), sc.id, sqlc.arg(prompt), sqlc.arg(style), sqlc.arg(notes), sqlc.narg(image_url),
		       COALESCE((SELECT MAX(position) + 1
		                 FROM shots WHERE scene_id = sc.id), 0),
		       sqlc.arg(user_id)
		FROM scenes sc
		WHERE sc.id = sqlc.arg(scene_id) AND sc.project_id = sqlc.arg(project_id)
		  AND EXISTS (
		    SELECT 1 FROM project_members pm
		    JOIN projects p ON p.id = pm.project_id
		    JOIN workspace_members wm
		      ON wm.workspace_id = p.workspace_id
		     AND wm.user_id = pm.user_id
		    WHERE pm.project_id = sqlc.arg(project_id) AND pm.user_id = sqlc.arg(user_id)
		      AND pm.role IN ('owner', 'editor')
		  )
		RETURNING id, scene_id, prompt, style, notes, image_url, position,
		          created_by, created_at, updated_at;

-- name: UpdateStoryboardShot :one
UPDATE shots s
		SET prompt=COALESCE(sqlc.narg(prompt),prompt),style=COALESCE(sqlc.narg(style),style),notes=COALESCE(sqlc.narg(notes),notes),image_url=COALESCE(sqlc.narg(image_url),image_url),position=COALESCE(sqlc.narg(position)::bigint,position),updated_at=NOW()
		FROM scenes sc
		WHERE s.id = sqlc.arg(shot_id)
		AND s.scene_id = sqlc.arg(scene_id)
		AND sc.id = s.scene_id
		AND sc.project_id = sqlc.arg(project_id)
		AND EXISTS (
			SELECT 1
			FROM project_members pm
			JOIN projects p ON p.id = pm.project_id
			JOIN workspace_members wm
			ON wm.workspace_id = p.workspace_id
			AND wm.user_id = pm.user_id
			WHERE pm.project_id = sqlc.arg(project_id)
			AND pm.user_id = sqlc.arg(user_id)
			AND pm.role IN ('owner', 'editor')
		)
		RETURNING s.id, s.scene_id, s.prompt, s.style, s.notes,
		          s.image_url, s.position, s.created_by,
		          s.created_at, s.updated_at;

-- name: DeleteStoryboardShot :execrows
DELETE FROM shots s
		USING scenes sc
		WHERE s.id = sqlc.arg(shot_id) AND s.scene_id = sqlc.arg(scene_id)
		  AND sc.id = s.scene_id AND sc.project_id = sqlc.arg(project_id)
		  AND EXISTS (
		    SELECT 1 FROM project_members pm
		    JOIN projects p ON p.id = pm.project_id
		    JOIN workspace_members wm
		      ON wm.workspace_id = p.workspace_id
		     AND wm.user_id = pm.user_id
		    WHERE pm.project_id = sqlc.arg(project_id) AND pm.user_id = sqlc.arg(user_id)
		      AND pm.role IN ('owner', 'editor')
		  );

-- name: CountProjectScenes :one
SELECT COUNT(*) FROM scenes WHERE project_id = sqlc.arg(project_id);

-- name: ReorderStoryboardScene :execrows
UPDATE scenes SET position = sqlc.arg(position)::bigint, updated_at = NOW()
			 WHERE id = sqlc.arg(id) AND project_id = sqlc.arg(project_id);

-- name: CountSceneShots :one
SELECT COUNT(*) FROM shots WHERE scene_id = sqlc.arg(scene_id);

-- name: ReorderStoryboardShot :execrows
UPDATE shots s SET position = sqlc.arg(position)::bigint, updated_at = NOW()
			 FROM scenes sc
			 WHERE s.id = sqlc.arg(id) AND s.scene_id = sqlc.arg(scene_id)
			   AND sc.id = s.scene_id AND sc.project_id = sqlc.arg(project_id);
