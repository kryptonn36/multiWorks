-- name: GetAssetShot :one
SELECT sh.id,sh.scene_id FROM shots sh JOIN scenes sc ON sc.id=sh.scene_id WHERE sc.project_id=sqlc.arg(project_id) AND sh.id=sqlc.arg(shot_id);

-- name: CreateAsset :one
INSERT INTO assets(project_id,sha256,mime_type,size_bytes,storage_key,created_by)
VALUES(sqlc.arg(project_id),sqlc.arg(sha256),sqlc.arg(mime_type),sqlc.arg(size_bytes),sqlc.arg(storage_key),sqlc.narg(created_by))
ON CONFLICT(project_id,sha256) DO NOTHING RETURNING *;

-- name: GetAssetByHash :one
SELECT * FROM assets WHERE project_id=sqlc.arg(project_id) AND sha256=sqlc.arg(sha256);

-- name: GetProjectAsset :one
SELECT * FROM assets WHERE project_id=sqlc.arg(project_id) AND id=sqlc.arg(asset_id);

-- name: GetWorkerAsset :one
SELECT * FROM assets WHERE id=sqlc.arg(asset_id);

-- name: ListProjectAssets :many
SELECT * FROM assets WHERE project_id=sqlc.arg(project_id) AND id>sqlc.arg(after_id) ORDER BY id LIMIT 101;

-- name: AttachShotAsset :exec
INSERT INTO shot_assets(project_id,shot_id,asset_id,attached_by)
SELECT sqlc.arg(project_id),sh.id,a.id,sqlc.narg(attached_by)
FROM shots sh JOIN scenes sc ON sc.id=sh.scene_id JOIN assets a ON a.project_id=sc.project_id
WHERE sc.project_id=sqlc.arg(project_id) AND sh.id=sqlc.arg(shot_id) AND a.id=sqlc.arg(asset_id)
ON CONFLICT(shot_id,asset_id) DO NOTHING;

-- name: EnqueueAssetJob :exec
INSERT INTO asset_jobs(asset_id,job_type) VALUES(sqlc.arg(asset_id),'thumbnail') ON CONFLICT(asset_id,job_type) DO NOTHING;

-- name: ClaimAssetJob :one
WITH candidate AS (
 SELECT id FROM asset_jobs
 WHERE (status='pending' AND available_at<=NOW()) OR (status='processing' AND locked_at<NOW()-INTERVAL '60 seconds')
 ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1
)
UPDATE asset_jobs j SET status='processing',attempts=j.attempts+1,locked_at=NOW(),updated_at=NOW()
FROM candidate WHERE j.id=candidate.id RETURNING j.*;

-- name: LockOwnedAssetJob :one
SELECT * FROM asset_jobs WHERE id=sqlc.arg(id) AND attempts=sqlc.arg(attempts) AND status='processing' FOR UPDATE;

-- name: CompleteAsset :one
UPDATE assets SET status='ready',thumbnail_key=sqlc.arg(thumbnail_key),error_message=NULL,updated_at=NOW() WHERE id=sqlc.arg(id) RETURNING *;

-- name: CompleteAssetJob :exec
UPDATE asset_jobs SET status='completed',last_error=NULL,updated_at=NOW() WHERE id=sqlc.arg(id);

-- name: FailAssetJob :exec
UPDATE asset_jobs SET status=sqlc.arg(status),last_error=sqlc.arg(last_error),available_at=NOW()+INTERVAL '1 second'*sqlc.arg(delay_seconds)::bigint,updated_at=NOW() WHERE id=sqlc.arg(id);

-- name: FailAsset :one
UPDATE assets SET status='failed',error_message=sqlc.arg(error_message),updated_at=NOW() WHERE id=sqlc.arg(id) RETURNING *;
