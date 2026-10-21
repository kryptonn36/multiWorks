package assets

import (
	"github.com/google/uuid"
	"github.com/kryptonn36/multiWorks/internal/db"
	"time"
)

const MaxUploadBytes int64 = 10 << 20
const MaxPixels = 4_000_000

type Asset struct {
	ID           uuid.UUID `json:"id"`
	ProjectID    uuid.UUID `json:"project_id"`
	SHA256       string    `json:"sha256"`
	MIMEType     string    `json:"mime_type"`
	SizeBytes    int64     `json:"size_bytes"`
	Status       string    `json:"status"`
	ThumbnailURL string    `json:"thumbnail_url,omitempty"`
	DownloadURL  string    `json:"download_url"`
	ErrorMessage string    `json:"error_message,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}
type UploadResult struct {
	Asset   Asset `json:"asset"`
	Deduped bool  `json:"deduped"`
}

func present(row db.Asset) Asset {
	base := "/api/projects/" + row.ProjectID.String() + "/assets/" + row.ID.String()
	item := Asset{ID: row.ID, ProjectID: row.ProjectID, SHA256: row.Sha256, MIMEType: row.MimeType, SizeBytes: row.SizeBytes, Status: row.Status, DownloadURL: base, ErrorMessage: row.ErrorMessage.String, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
	if row.Status == "ready" && row.ThumbnailKey.Valid {
		item.ThumbnailURL = base + "/thumbnail"
	}
	return item
}
