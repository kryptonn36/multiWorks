package assets

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/kryptonn36/multiWorks/internal/ops"
)

type Handler struct{ service *Service }

func NewHandler(service *Service) *Handler { return &Handler{service: service} }
func ids(c *gin.Context) (uuid.UUID, uuid.UUID, bool) {
	value, ok := c.Get("userID")
	user, valid := value.(uuid.UUID)
	if !ok || !valid || user == uuid.Nil {
		c.JSON(401, gin.H{"error": "authentication required"})
		return uuid.Nil, uuid.Nil, false
	}
	project, err := uuid.Parse(c.Param("projectID"))
	if err != nil || project == uuid.Nil {
		c.JSON(404, gin.H{"error": "project not found"})
		return uuid.Nil, uuid.Nil, false
	}
	return project, user, true
}
func fail(c *gin.Context, err error) {
	code := ops.ErrorStatus(err)
	message := err.Error()
	if code >= 500 {
		message = "could not complete asset request"
	}
	c.JSON(code, gin.H{"error": message})
}
func (h *Handler) Upload(c *gin.Context) {
	project, user, ok := ids(c)
	if !ok {
		return
	}
	if err := h.service.Access(c.Request.Context(), project, user, true); err != nil {
		fail(c, err)
		return
	}
	shot, err := uuid.Parse(c.Param("shotID"))
	if err != nil || shot == uuid.Nil {
		c.JSON(404, gin.H{"error": "shot not found"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxUploadBytes+(64<<10))
	if err := c.Request.ParseMultipartForm(MaxUploadBytes); err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			c.JSON(413, gin.H{"error": "image exceeds the upload limit"})
		} else {
			c.JSON(400, gin.H{"error": "send a multipart image in the file field"})
		}
		return
	}
	if c.Request.MultipartForm != nil {
		defer c.Request.MultipartForm.RemoveAll()
	}
	file, _, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(400, gin.H{"error": "file is required"})
		return
	}
	defer file.Close()
	result, err := h.service.Upload(c.Request.Context(), project, shot, user, file)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(201, result)
}
func (h *Handler) List(c *gin.Context) {
	project, user, ok := ids(c)
	if !ok {
		return
	}
	after := uuid.Nil
	if value := c.Query("after_id"); value != "" {
		var err error
		after, err = uuid.Parse(value)
		if err != nil {
			c.JSON(400, gin.H{"error": "invalid asset cursor"})
			return
		}
	}
	items, more, err := h.service.List(c.Request.Context(), project, user, after)
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(200, gin.H{"assets": items, "has_more": more})
}
func (h *Handler) Download(c *gin.Context)  { h.download(c, false) }
func (h *Handler) Thumbnail(c *gin.Context) { h.download(c, true) }
func (h *Handler) download(c *gin.Context, thumbnail bool) {
	project, user, ok := ids(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("assetID"))
	if err != nil || id == uuid.Nil {
		c.JSON(404, gin.H{"error": "asset not found"})
		return
	}
	item, err := h.service.Get(c.Request.Context(), project, id, user)
	if err != nil {
		fail(c, err)
		return
	}
	key, mime := item.StorageKey, item.MimeType
	if thumbnail {
		if item.Status != "ready" || !item.ThumbnailKey.Valid {
			c.JSON(409, gin.H{"error": "thumbnail is not ready"})
			return
		}
		key = item.ThumbnailKey.String
		mime = "image/jpeg"
	}
	file, err := h.service.Storage.Open(key)
	if err != nil {
		c.JSON(404, gin.H{"error": "asset file not found"})
		return
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		c.JSON(500, gin.H{"error": "could not read asset"})
		return
	}
	c.Header("Content-Type", mime)
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Cache-Control", "private, no-store")
	http.ServeContent(c.Writer, c.Request, id.String(), stat.ModTime(), file)
}
