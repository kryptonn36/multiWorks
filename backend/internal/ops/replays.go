package ops

import (
	"database/sql"
	"errors"
	"github.com/kryptonn36/multiWorks/internal/db"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// Replay returns operations after a client's last processed sequence.
//
// GET /api/projects/:projectID/ops?after_seq=12&limit=100
func (h *Handler) Replay(c *gin.Context) {
	userID, ok := userIDFromContext(c)
	if !ok {
		fail(c, http.StatusUnauthorized, "authentication required")
		return
	}

	projectID, ok := parseID(c.Param("projectID"))
	if !ok {
		fail(c, http.StatusNotFound, "project not found")
		return
	}

	afterSeq, err := strconv.ParseInt(c.DefaultQuery("after_seq", "0"), 10, 64)
	if err != nil || afterSeq < 0 {
		fail(c, http.StatusBadRequest, "after_seq must be a non-negative integer")
		return
	}

	limit := 100
	if raw := c.Query("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 500 {
			fail(c, http.StatusBadRequest, "limit must be between 1 and 500")
			return
		}
	}

	ctx := c.Request.Context()

	currentSeq, err := db.New(h.database).GetReplaySequence(ctx, db.GetReplaySequenceParams{ProjectID: projectID, UserID: userID})

	if errors.Is(err, sql.ErrNoRows) {
		fail(c, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		fail(c, http.StatusInternalServerError, "could not load project")
		return
	}

	rows, err := db.New(h.database).ListReplayOperations(ctx, db.ListReplayOperationsParams{ProjectID: projectID, AfterSeq: afterSeq, RowLimit: int32(limit + 1)})
	if err != nil {
		fail(c, http.StatusInternalServerError, "could not replay operations")
		return
	}
	operations := make([]Operation, 0, len(rows))
	for _, row := range rows {
		operations = append(operations, Operation{ID: row.ID, ProjectID: row.ProjectID, Seq: row.Seq, ClientID: row.ClientID, OpID: row.OpID, ActorID: row.ActorID, Type: row.OpType, Payload: row.Payload, CreatedAt: row.CreatedAt})
	}

	hasMore := len(operations) > limit
	if hasMore {
		operations = operations[:limit]
	}

	c.JSON(http.StatusOK, ReplayResponse{
		Operations: operations,
		CurrentSeq: currentSeq,
		HasMore:    hasMore,
	})
}
