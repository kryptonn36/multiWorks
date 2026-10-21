package assets

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"image"
	"image/jpeg"
	"io"
	"log"
	"time"

	"github.com/kryptonn36/multiWorks/internal/db"
	"golang.org/x/image/draw"
)

// Run uses durable leased jobs; interrupted processing is reclaimed after 60s.
func (s *Service) Run(ctx context.Context) {
	for ctx.Err() == nil {
		processed, err := s.ProcessOne(ctx)
		if err != nil && ctx.Err() == nil {
			log.Printf("asset worker: %v", err)
		}
		if !processed || err != nil {
			timer := time.NewTimer(500 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}
}
func (s *Service) ProcessOne(parent context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(parent, 25*time.Second)
	defer cancel()
	// ClaimAssetJob atomically locks/skips/claims a single row in one statement.
	job, err := db.New(s.Database).ClaimAssetJob(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	asset, err := db.New(s.Database).GetWorkerAsset(ctx, job.AssetID)
	if err != nil {
		return true, err
	}
	key := "thumbnails/" + asset.ProjectID.String() + "/" + asset.Sha256[:2] + "/" + asset.Sha256[2:4] + "/" + asset.Sha256 + ".jpg"
	processingErr := func() error {
		if job.Attempts > 3 {
			return errors.New("thumbnail retry limit exhausted")
		}
		file, err := s.Storage.Open(asset.StorageKey)
		if err != nil {
			return err
		}
		defer file.Close()
		raw, err := io.ReadAll(io.LimitReader(file, s.Limits.MaxUploadBytes+1))
		if err != nil {
			return err
		}
		if int64(len(raw)) > s.Limits.MaxUploadBytes {
			return errors.New("original exceeds upload limit")
		}
		source, _, err := s.decode(raw)
		if err != nil {
			return err
		}
		bounds := source.Bounds()
		width, height := bounds.Dx(), bounds.Dy()
		if width > 320 || height > 320 {
			if width >= height {
				height = max(1, height*320/width)
				width = 320
			} else {
				width = max(1, width*320/height)
				height = 320
			}
		}
		thumb := image.NewRGBA(image.Rect(0, 0, width, height))
		draw.CatmullRom.Scale(thumb, thumb.Bounds(), source, bounds, draw.Over, nil)
		var encoded bytes.Buffer
		if err := jpeg.Encode(&encoded, thumb, &jpeg.Options{Quality: 85}); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return s.Storage.Put(key, &encoded)
	}()
	// A fencing check prevents an expired worker from overwriting a newer attempt.
	finishCtx, finishCancel := context.WithTimeout(parent, 5*time.Second)
	defer finishCancel()
	tx, err := s.Database.BeginTx(finishCtx, nil)
	if err != nil {
		return true, err
	}
	defer tx.Rollback()
	queries := db.New(s.Database).WithTx(tx)
	if _, err := queries.LockOwnedAssetJob(finishCtx, db.LockOwnedAssetJobParams{ID: job.ID, Attempts: job.Attempts}); errors.Is(err, sql.ErrNoRows) {
		return true, nil
	} else if err != nil {
		return true, err
	}
	var update db.Asset
	if processingErr == nil {
		update, err = queries.CompleteAsset(finishCtx, db.CompleteAssetParams{ID: asset.ID, ThumbnailKey: sql.NullString{String: key, Valid: true}})
		if err == nil {
			err = queries.CompleteAssetJob(finishCtx, job.ID)
		}
	} else {
		safe := "Image processing failed. Please try another image."
		status := "pending"
		if job.Attempts >= 3 {
			status = "failed"
		}
		err = queries.FailAssetJob(finishCtx, db.FailAssetJobParams{ID: job.ID, Status: status, LastError: sql.NullString{String: safe, Valid: true}, DelaySeconds: int64(1 << min(job.Attempts, 3))})
		if err == nil && status == "failed" {
			update, err = queries.FailAsset(finishCtx, db.FailAssetParams{ID: asset.ID, ErrorMessage: sql.NullString{String: safe, Valid: true}})
		}
	}
	if err != nil {
		return true, err
	}
	if err := tx.Commit(); err != nil {
		return true, err
	}
	if update.ID == asset.ID && s.Publish != nil {
		s.Publish(asset.ProjectID, present(update))
	}
	return true, nil
}
