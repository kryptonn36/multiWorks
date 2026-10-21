package agent

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/kryptonn36/multiWorks/internal/ops"
	"net/http"
)

type Handler struct{ Service *Service }

func NewHandler(service *Service) *Handler { return &Handler{Service: service} }
func fail(c *gin.Context, err error) {
	status := ops.ErrorStatus(err)
	message := err.Error()
	if status >= 500 {
		message = "could not complete agent request"
	}
	c.JSON(status, gin.H{"error": message})
}
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
func (h *Handler) Generate(c *gin.Context) {
	project, user, ok := ids(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16384)
	var req ProposeRequest
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(400, gin.H{"error": "invalid agent request"})
		return
	}
	item, err := h.Service.Propose(c.Request.Context(), project, user, req.Instruction, req.Mode)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(202, item)
}
func (h *Handler) List(c *gin.Context) {
	project, user, ok := ids(c)
	if !ok {
		return
	}
	after := uuid.Nil
	if cursor := c.Query("after_id"); cursor != "" {
		var err error
		after, err = uuid.Parse(cursor)
		if err != nil {
			c.JSON(400, gin.H{"error": "invalid after_id"})
			return
		}
	}
	items, more, err := h.Service.List(c.Request.Context(), project, user, after)
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(200, gin.H{"proposals": items, "has_more": more})
}
func (h *Handler) Get(c *gin.Context)     { h.item(c, "get") }
func (h *Handler) Approve(c *gin.Context) { h.item(c, "approve") }
func (h *Handler) Reject(c *gin.Context)  { h.item(c, "reject") }
func (h *Handler) item(c *gin.Context, action string) {
	project, user, ok := ids(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("proposalID"))
	if err != nil || id == uuid.Nil {
		c.JSON(404, gin.H{"error": "proposal not found"})
		return
	}
	var item Proposal
	if action == "get" {
		item, err = h.Service.Get(c.Request.Context(), project, user, id)
	} else {
		item, err = h.Service.Decide(c.Request.Context(), project, user, id, action == "approve")
	}
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(200, item)
}
