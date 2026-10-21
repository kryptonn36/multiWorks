-- name: LoadBoardState :one
SELECT jsonb_build_object(
    'project_id', p.id,
    'last_seq', p.last_seq,
    'scenes', COALESCE((
        SELECT jsonb_agg(
            to_jsonb(sc) || jsonb_build_object('shots', COALESCE((
                SELECT jsonb_agg(to_jsonb(sh) || jsonb_build_object('asset_ids',COALESCE((SELECT jsonb_agg(sa.asset_id ORDER BY sa.created_at,sa.asset_id) FROM shot_assets sa WHERE sa.shot_id=sh.id),'[]'::jsonb)) ORDER BY sh.position, sh.created_at, sh.id)
                FROM shots sh
                WHERE sh.scene_id = sc.id
            ), '[]'::jsonb))
            ORDER BY sc.position, sc.created_at, sc.id
        )
        FROM scenes sc
        WHERE sc.project_id = p.id
    ), '[]'::jsonb)
)
FROM projects p
WHERE p.id = sqlc.arg(project_id);

-- name: DeleteBoardScenes :execrows
DELETE FROM scenes WHERE project_id=sqlc.arg(project_i_d);

-- name: RestoreScene :execrows
INSERT INTO scenes(id,project_id,title,description,position,created_by,created_at,updated_at) VALUES(sqlc.arg(id),sqlc.arg(project_i_d),sqlc.arg(title),sqlc.arg(description),sqlc.arg(position)::bigint,sqlc.arg(created_by),sqlc.arg(created_at),sqlc.arg(updated_at));

-- name: RestoreShot :execrows
INSERT INTO shots(id,scene_id,prompt,style,notes,image_url,position,created_by,created_at,updated_at) VALUES(sqlc.arg(id),sqlc.arg(scene_id),sqlc.arg(prompt),sqlc.arg(style),sqlc.arg(notes),sqlc.narg(image_url),sqlc.arg(position)::bigint,sqlc.arg(created_by),sqlc.arg(created_at),sqlc.arg(updated_at));
