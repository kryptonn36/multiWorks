package history

import (
	"math"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/kryptonn36/multiWorks/internal/ops"
)

type Handler struct{ service *Service }

func NewHandler(service *Service) *Handler { return &Handler{service: service} }
func identifiers(c *gin.Context) (uuid.UUID, uuid.UUID, bool) {
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
func sequence(c *gin.Context) (int64, bool) {
	seq, err := strconv.ParseInt(c.Param("seq"), 10, 64)
	if err != nil || seq < 0 {
		c.JSON(400, gin.H{"error": "seq must be a non-negative integer"})
		return 0, false
	}
	return seq, true
}
func respondError(c *gin.Context, err error) {
	code := status(err)
	message := err.Error()
	if code == 404 {
		message = "project not found"
	}
	if code >= 500 {
		message = "could not load project history"
	}
	c.JSON(code, gin.H{"error": message})
}
func (h *Handler) List(c *gin.Context) {
	project, user, ok := identifiers(c)
	if !ok {
		return
	}
	before := int64(math.MaxInt64)
	if value := c.Query("before_seq"); value != "" {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed < 0 {
			c.JSON(400, gin.H{"error": "before_seq must be a non-negative integer"})
			return
		}
		before = parsed
	}
	result, err := h.service.List(c.Request.Context(), project, user, before)
	if err != nil {
		respondError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(200, result)
}
func (h *Handler) GetAtSequence(c *gin.Context) {
	project, user, ok := identifiers(c)
	if !ok {
		return
	}
	seq, ok := sequence(c)
	if !ok {
		return
	}
	result, err := h.service.GetAtSequence(c.Request.Context(), project, user, seq)
	if err != nil {
		respondError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(200, result)
}
func (h *Handler) Restore(c *gin.Context) {
	project, user, ok := identifiers(c)
	if !ok {
		return
	}
	seq, ok := sequence(c)
	if !ok {
		return
	}
	var body struct {
		ClientID string `json:"client_id"`
		OpID     string `json:"op_id"`
		BaseSeq  *int64 `json:"base_seq"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.BaseSeq == nil {
		c.JSON(400, gin.H{"error": "client_id, op_id, and base_seq are required"})
		return
	}
	result, err := h.service.Restore(c.Request.Context(), project, user, seq, ops.SubmitRequest{ClientID: body.ClientID, OpID: body.OpID, BaseSeq: *body.BaseSeq})
	if err != nil {
		respondError(c, err)
		return
	}
	code := 201
	if result.Duplicate {
		code = 200
	}
	c.JSON(code, result)
}
