package assets

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"

	"github.com/google/uuid"
	"github.com/kryptonn36/multiWorks/internal/db"
	"github.com/kryptonn36/multiWorks/internal/ops"
	_ "golang.org/x/image/webp"
)

type Service struct {
	Database   *sql.DB
	Storage    *Storage
	Operations *ops.Service
	Publish    func(uuid.UUID, Asset)
}

func NewService(database *sql.DB, storage *Storage, operations *ops.Service) *Service {
	return &Service{Database: database, Storage: storage, Operations: operations}
}
func (s *Service) Access(ctx context.Context, project, user uuid.UUID, write bool) error {
	if user == uuid.Nil {
		return ops.NewError(401, "authentication required")
	}
	access, err := db.New(s.Database).GetHistoryAccess(ctx, db.GetHistoryAccessParams{ProjectID: project, UserID: user})
	if errors.Is(err, sql.ErrNoRows) {
		return ops.NewError(404, "project not found")
	}
	if err != nil {
		return err
	}
	if write && access.Role != "owner" && access.Role != "editor" {
		return ops.NewError(403, "editor or owner role required")
	}
	return nil
}
func decode(raw []byte) (image.Image, string, error) {
	config, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || (format != "jpeg" && format != "png" && format != "webp") {
		return nil, "", ops.NewError(400, "upload a valid JPEG, PNG, or WebP image")
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > 8192 || config.Height > 8192 || int64(config.Width)*int64(config.Height) > MaxPixels {
		return nil, "", ops.NewError(400, "image dimensions exceed the 4 million pixel limit")
	}
	decoded, actual, err := image.Decode(bytes.NewReader(raw))
	if err != nil || actual != format {
		return nil, "", ops.NewError(400, "image could not be decoded")
	}
	return decoded, format, nil
}
func (s *Service) Upload(ctx context.Context, project, shot, user uuid.UUID, reader io.Reader) (UploadResult, error) {
	var result UploadResult
	if err := s.Access(ctx, project, user, true); err != nil {
		return result, err
	}
	if _, err := db.New(s.Database).GetAssetShot(ctx, db.GetAssetShotParams{ProjectID: project, ShotID: shot}); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return result, ops.NewError(404, "shot not found")
		}
		return result, err
	}
	raw, err := io.ReadAll(io.LimitReader(reader, MaxUploadBytes+1))
	if err != nil {
		return result, ops.NewError(400, "could not read uploaded image")
	}
	if int64(len(raw)) > MaxUploadBytes {
		return result, ops.NewError(413, "image exceeds the 10 MiB upload limit")
	}
	_, format, err := decode(raw)
	if err != nil {
		return result, err
	}
	digest := sha256.Sum256(raw)
	hash := hex.EncodeToString(digest[:])
	ext := format
	if format == "jpeg" {
		ext = "jpg"
	}
	key := "originals/" + project.String() + "/" + hash[:2] + "/" + hash[2:4] + "/" + hash + "." + ext
	var row db.Asset
	_, err = s.Operations.Transact(ctx, project, user, func(tx *sql.Tx, apply func(ops.SubmitRequest) (ops.SubmitResponse, error)) error {
		queries := db.New(s.Database).WithTx(tx)
		target, err := queries.GetAssetShot(ctx, db.GetAssetShotParams{ProjectID: project, ShotID: shot})
		if errors.Is(err, sql.ErrNoRows) {
			return ops.NewError(404, "shot not found")
		}
		if err != nil {
			return err
		}
		if err := s.Storage.Put(key, bytes.NewReader(raw)); err != nil {
			return err
		}
		row, err = queries.CreateAsset(ctx, db.CreateAssetParams{ProjectID: project, Sha256: hash, MimeType: "image/" + format, SizeBytes: int64(len(raw)), StorageKey: key, CreatedBy: uuid.NullUUID{UUID: user, Valid: true}})
		if errors.Is(err, sql.ErrNoRows) {
			result.Deduped = true
			row, err = queries.GetAssetByHash(ctx, db.GetAssetByHashParams{ProjectID: project, Sha256: hash})
		} else if err == nil {
			err = queries.EnqueueAssetJob(ctx, row.ID)
		}
		if err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]any{"scene_id": target.SceneID, "shot_id": shot, "asset_id": row.ID})
		_, err = apply(ops.SubmitRequest{ClientID: "asset-upload", OpID: uuid.NewString(), Type: "asset.attach", Payload: payload})
		return err
	})
	if err != nil {
		return result, err
	}
	result.Asset = present(row)
	if s.Publish != nil {
		s.Publish(project, result.Asset)
	}
	return result, nil
}
func (s *Service) List(ctx context.Context, project, user, after uuid.UUID) ([]Asset, bool, error) {
	if err := s.Access(ctx, project, user, false); err != nil {
		return nil, false, err
	}
	rows, err := db.New(s.Database).ListProjectAssets(ctx, db.ListProjectAssetsParams{ProjectID: project, AfterID: after})
	if err != nil {
		return nil, false, err
	}
	more := len(rows) > 100
	if more {
		rows = rows[:100]
	}
	items := make([]Asset, 0, len(rows))
	for _, row := range rows {
		items = append(items, present(row))
	}
	return items, more, nil
}
func (s *Service) Get(ctx context.Context, project, asset, user uuid.UUID) (db.Asset, error) {
	if err := s.Access(ctx, project, user, false); err != nil {
		return db.Asset{}, err
	}
	row, err := db.New(s.Database).GetProjectAsset(ctx, db.GetProjectAssetParams{ProjectID: project, AssetID: asset})
	if errors.Is(err, sql.ErrNoRows) {
		return row, ops.NewError(404, "asset not found")
	}
	return row, err
}
