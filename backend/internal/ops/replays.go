
package ops

import (
	"database/sql"
	"errors"
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

	var currentSeq int64
	err = h.database.QueryRowContext(ctx, `
		SELECT last_seq
		FROM projects
		WHERE id = $1
		  AND EXISTS (
		    SELECT 1
		    FROM project_members pm
		    JOIN workspace_members wm
		      ON wm.workspace_id = projects.workspace_id
		     AND wm.user_id = pm.user_id
		    WHERE pm.project_id = projects.id
		      AND pm.user_id = $2
		  )
	`, projectID, userID).Scan(&currentSeq)

	if errors.Is(err, sql.ErrNoRows) {
		fail(c, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		fail(c, http.StatusInternalServerError, "could not load project")
		return
	}

	rows, err := h.database.QueryContext(ctx, `
		SELECT id, project_id, seq, client_id, op_id, actor_id,
		       op_type, payload, created_at
		FROM project_ops
		WHERE project_id = $1 AND seq > $2
		ORDER BY seq ASC
		LIMIT $3
	`, projectID, afterSeq, limit+1)
	if err != nil {
		fail(c, http.StatusInternalServerError, "could not replay operations")
		return
	}
	defer rows.Close()

	operations := make([]Operation, 0, limit)
	for rows.Next() {
		var op Operation
		var payload []byte

		if err := rows.Scan(
			&op.ID,
			&op.ProjectID,
			&op.Seq,
			&op.ClientID,
			&op.OpID,
			&op.ActorID,
			&op.Type,
			&payload,
			&op.CreatedAt,
		); err != nil {
			fail(c, http.StatusInternalServerError, "could not read operation history")
			return
		}
		op.Payload = payload
		operations = append(operations, op)
	}
	if err := rows.Err(); err != nil {
		fail(c, http.StatusInternalServerError, "could not read operation history")
		return
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
