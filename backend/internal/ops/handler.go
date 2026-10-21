package ops

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type Handler struct {
	database *sql.DB
	service  *Service
}

func NewHandler(database *sql.DB) *Handler {
	return NewHandlerWithService(NewService(database))
}

// NewHandlerWithService lets REST and other transports share one operation pipeline.
func NewHandlerWithService(service *Service) *Handler {
	return &Handler{database: service.database, service: service}
}

type payload struct {
	SceneID     string   `json:"scene_id"`
	ShotID      string   `json:"shot_id"`
	Title       *string  `json:"title"`
	Description *string  `json:"description"`
	Prompt      *string  `json:"prompt"`
	Style       *string  `json:"style"`
	Notes       *string  `json:"notes"`
	ImageURL    *string  `json:"image_url"`
	SceneIDs    []string `json:"scene_ids"`
	ShotIDs     []string `json:"shot_ids"`
}

func (h *Handler) SetPublisher(
	publisher func(projectID uuid.UUID, operation Operation),
) {
	h.service.SetPublisher(publisher)
}

func fail(c *gin.Context, status int, message string) {
	c.JSON(status, gin.H{"error": message})
}

func userIDFromContext(c *gin.Context) (uuid.UUID, bool) {
	value, ok := c.Get("userID")
	if !ok {
		return uuid.Nil, false
	}
	id, ok := value.(uuid.UUID)
	return id, ok && id != uuid.Nil
}

func parseID(value string) (uuid.UUID, bool) {
	id, err := uuid.Parse(value)
	return id, err == nil && id != uuid.Nil
}

func validName(value string, max int) bool {
	value = strings.TrimSpace(value)
	return len(value) > 0 && len(value) <= max
}

func validOptional(value *string, max int) bool {
	return value == nil || len(*value) <= max
}

func decodePayload(raw json.RawMessage) (payload, error) {
	var p payload
	if len(raw) == 0 || !json.Valid(raw) {
		return p, errors.New("invalid JSON payload")
	}
	err := json.Unmarshal(raw, &p)
	return p, err
}

func validateRequest(req SubmitRequest) error {
	if len(req.ClientID) == 0 || len(req.ClientID) > 100 {
		return errors.New("client_id must be 1-100 characters")
	}
	if len(req.OpID) == 0 || len(req.OpID) > 150 {
		return errors.New("op_id must be 1-150 characters")
	}
	if req.BaseSeq < 0 {
		return errors.New("base_seq cannot be negative")
	}
	if len(req.Type) == 0 || len(req.Type) > 50 {
		return errors.New("invalid operation type")
	}
	if len(req.Payload) == 0 || len(req.Payload) > 64*1024 {
		return errors.New("payload is required and must be at most 64 KiB")
	}
	return nil
}

func requireUUID(value string) (uuid.UUID, error) {
	id, ok := parseID(value)
	if !ok {
		return uuid.Nil, errors.New("valid UUID required")
	}
	return id, nil
}

func projectRoleTx(
	ctx context.Context,
	tx *sql.Tx,
	projectID, userID uuid.UUID,
) (string, error) {
	var role string
	err := tx.QueryRowContext(ctx, `
		SELECT pm.role
		FROM project_members pm
		JOIN projects p ON p.id = pm.project_id
		JOIN workspace_members wm
		  ON wm.workspace_id = p.workspace_id
		 AND wm.user_id = pm.user_id
		WHERE pm.project_id = $1
		  AND pm.user_id = $2
	`, projectID, userID).Scan(&role)
	return role, err
}

// Submit is the single write entry point for storyboard operations.
//
// POST /api/projects/:projectID/ops
func (h *Handler) Submit(c *gin.Context) {
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

	var req SubmitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid operation request")
		return
	}
	result, err := h.service.Submit(c.Request.Context(), projectID, userID, req)
	if err != nil {
		fail(c, ErrorStatus(err), err.Error())
		return
	}
	status := http.StatusCreated
	if result.Duplicate {
		status = http.StatusOK
	}
	c.JSON(status, result)
}

type operationError struct {
	status  int
	message string
}

func (e *operationError) Error() string {
	return e.message
}

func opError(status int, message string) error {
	return &operationError{status: status, message: message}
}

func ensureScene(
	ctx context.Context,
	tx *sql.Tx,
	projectID, sceneID uuid.UUID,
) error {
	var exists bool
	err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(
		    SELECT 1 FROM scenes
		    WHERE id = $1 AND project_id = $2
		)
	`, sceneID, projectID).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return opError(http.StatusConflict, "scene does not exist in this project")
	}
	return nil
}

func ensureShot(
	ctx context.Context,
	tx *sql.Tx,
	projectID, sceneID, shotID uuid.UUID,
) error {
	var exists bool
	err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(
		    SELECT 1
		    FROM shots s
		    JOIN scenes sc ON sc.id = s.scene_id
		    WHERE s.id = $1
		      AND s.scene_id = $2
		      AND sc.project_id = $3
		)
	`, shotID, sceneID, projectID).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return opError(http.StatusConflict, "shot was deleted or does not belong to this scene")
	}
	return nil
}

func applyOperation(
	ctx context.Context,
	tx *sql.Tx,
	projectID, userID uuid.UUID,
	opType string,
	p payload,
) error {
	switch opType {
	case "scene.create":
		sceneID, err := requireUUID(p.SceneID)
		if err != nil {
			return opError(http.StatusBadRequest, "valid scene_id is required")
		}
		if p.Title == nil || !validName(*p.Title, 200) || !validOptional(p.Description, 5000) {
			return opError(http.StatusBadRequest, "valid title and description are required")
		}

		_, err = tx.ExecContext(ctx, `
			INSERT INTO scenes (
				id, project_id, title, description, position, created_by
			)
			VALUES (
				$1, $2, $3, $4,
				COALESCE(
					(SELECT MAX(position) + 1 FROM scenes WHERE project_id = $2),
					0
				),
				$5
			)
		`, sceneID, projectID, strings.TrimSpace(*p.Title),
			valueOrEmpty(p.Description), userID)
		return err

	case "scene.update":
		sceneID, err := requireUUID(p.SceneID)
		if err != nil {
			return opError(http.StatusBadRequest, "valid scene_id is required")
		}
		if p.Title == nil && p.Description == nil {
			return opError(http.StatusBadRequest, "provide title or description")
		}
		if p.Title != nil && !validName(*p.Title, 200) {
			return opError(http.StatusBadRequest, "invalid scene title")
		}
		if p.Description != nil && len(*p.Description) > 5000 {
			return opError(http.StatusBadRequest, "scene description is too long")
		}
		if err := ensureScene(ctx, tx, projectID, sceneID); err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, `
			UPDATE scenes
			SET title = COALESCE($3, title),
			    description = COALESCE($4, description),
			    updated_at = NOW()
			WHERE id = $1 AND project_id = $2
		`, sceneID, projectID, p.Title, p.Description)
		return err

	case "scene.delete":
		sceneID, err := requireUUID(p.SceneID)
		if err != nil {
			return opError(http.StatusBadRequest, "valid scene_id is required")
		}
		result, err := tx.ExecContext(ctx, `
			DELETE FROM scenes WHERE id = $1 AND project_id = $2
		`, sceneID, projectID)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return opError(http.StatusConflict, "scene was already deleted")
		}
		return nil

	case "shot.create":
		sceneID, err := requireUUID(p.SceneID)
		if err != nil {
			return opError(http.StatusBadRequest, "valid scene_id is required")
		}
		shotID, err := requireUUID(p.ShotID)
		if err != nil {
			return opError(http.StatusBadRequest, "valid shot_id is required")
		}
		if len(valueOrEmpty(p.Prompt)) > 10000 ||
			len(valueOrEmpty(p.Style)) > 500 ||
			len(valueOrEmpty(p.Notes)) > 5000 {
			return opError(http.StatusBadRequest, "shot fields exceed allowed limits")
		}
		if err := ensureScene(ctx, tx, projectID, sceneID); err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, `
			INSERT INTO shots (
			    id, scene_id, prompt, style, notes, image_url, position, created_by
			)
			VALUES (
			    $1, $2, $3, $4, $5, $6,
			    COALESCE((SELECT MAX(position) + 1 FROM shots WHERE scene_id = $2), 0),
			    $7
			)
		`, shotID, sceneID, valueOrEmpty(p.Prompt), valueOrEmpty(p.Style),
			valueOrEmpty(p.Notes), p.ImageURL, userID)
		return err

	case "shot.update":
		sceneID, err := requireUUID(p.SceneID)
		if err != nil {
			return opError(http.StatusBadRequest, "valid scene_id is required")
		}
		shotID, err := requireUUID(p.ShotID)
		if err != nil {
			return opError(http.StatusBadRequest, "valid shot_id is required")
		}
		if p.Prompt == nil && p.Style == nil && p.Notes == nil && p.ImageURL == nil {
			return opError(http.StatusBadRequest, "provide at least one shot field")
		}
		if !validOptional(p.Prompt, 10000) ||
			!validOptional(p.Style, 500) ||
			!validOptional(p.Notes, 5000) {
			return opError(http.StatusBadRequest, "shot fields exceed allowed limits")
		}
		if err := ensureShot(ctx, tx, projectID, sceneID, shotID); err != nil {
			return err
		}

		// Field-level last-writer-wins: each operation changes only fields supplied.
		// An omitted field is preserved.
		if p.ImageURL != nil {
			_, err = tx.ExecContext(ctx, `
				UPDATE shots
				SET prompt = COALESCE($4, prompt),
				    style = COALESCE($5, style),
				    notes = COALESCE($6, notes),
				    image_url = $7,
				    updated_at = NOW()
				WHERE id = $1 AND scene_id = $2
				  AND EXISTS (
				    SELECT 1 FROM scenes WHERE id = $2 AND project_id = $3
				  )
			`, shotID, sceneID, projectID, p.Prompt, p.Style, p.Notes, p.ImageURL)
		} else {
			_, err = tx.ExecContext(ctx, `
				UPDATE shots
				SET prompt = COALESCE($4, prompt),
				    style = COALESCE($5, style),
				    notes = COALESCE($6, notes),
				    updated_at = NOW()
				WHERE id = $1 AND scene_id = $2
				  AND EXISTS (
				    SELECT 1 FROM scenes WHERE id = $2 AND project_id = $3
				  )
			`, shotID, sceneID, projectID, p.Prompt, p.Style, p.Notes)
		}
		return err

	case "shot.delete":
		sceneID, err := requireUUID(p.SceneID)
		if err != nil {
			return opError(http.StatusBadRequest, "valid scene_id is required")
		}
		shotID, err := requireUUID(p.ShotID)
		if err != nil {
			return opError(http.StatusBadRequest, "valid shot_id is required")
		}
		result, err := tx.ExecContext(ctx, `
			DELETE FROM shots s
			USING scenes sc
			WHERE s.id = $1 AND s.scene_id = $2
			  AND sc.id = s.scene_id AND sc.project_id = $3
		`, shotID, sceneID, projectID)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return opError(http.StatusConflict, "shot was already deleted")
		}
		return nil

	case "scene.reorder":
		ids, err := parseIDs(p.SceneIDs, 500)
		if err != nil {
			return opError(http.StatusBadRequest, err.Error())
		}
		var count int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM scenes WHERE project_id = $1`,
			projectID,
		).Scan(&count); err != nil {
			return err
		}
		if count != len(ids) {
			return opError(http.StatusConflict, "scene order must include every scene exactly once")
		}
		for pos, id := range ids {
			result, err := tx.ExecContext(ctx, `
				UPDATE scenes SET position = $1, updated_at = NOW()
				WHERE id = $2 AND project_id = $3
			`, pos, id, projectID)
			if err != nil {
				return err
			}
			n, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if n != 1 {
				return opError(http.StatusConflict, "scene order contains a scene from another project")
			}
		}
		return nil

	case "shot.reorder":
		sceneID, err := requireUUID(p.SceneID)
		if err != nil {
			return opError(http.StatusBadRequest, "valid scene_id is required")
		}
		if err := ensureScene(ctx, tx, projectID, sceneID); err != nil {
			return err
		}
		ids, err := parseIDs(p.ShotIDs, 1000)
		if err != nil {
			return opError(http.StatusBadRequest, err.Error())
		}
		var count int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM shots WHERE scene_id = $1`,
			sceneID,
		).Scan(&count); err != nil {
			return err
		}
		if count != len(ids) {
			return opError(http.StatusConflict, "shot order must include every shot exactly once")
		}
		for pos, id := range ids {
			result, err := tx.ExecContext(ctx, `
				UPDATE shots s SET position = $1, updated_at = NOW()
				WHERE s.id = $2 AND s.scene_id = $3
				  AND EXISTS (
				    SELECT 1 FROM scenes WHERE id = $3 AND project_id = $4
				  )
			`, pos, id, sceneID, projectID)
			if err != nil {
				return err
			}
			n, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if n != 1 {
				return opError(http.StatusConflict, "shot order contains a shot from another scene")
			}
		}
		return nil

	default:
		return opError(http.StatusBadRequest, fmt.Sprintf("unsupported operation type: %s", opType))
	}
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func parseIDs(values []string, max int) ([]uuid.UUID, error) {
	if len(values) == 0 || len(values) > max {
		return nil, fmt.Errorf("IDs must contain between 1 and %d entries", max)
	}
	result := make([]uuid.UUID, 0, len(values))
	seen := make(map[uuid.UUID]struct{}, len(values))
	for _, value := range values {
		id, ok := parseID(value)
		if !ok {
			return nil, errors.New("all IDs must be valid UUIDs")
		}
		if _, exists := seen[id]; exists {
			return nil, errors.New("duplicate IDs are not allowed")
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result, nil
}

func jsonEqual(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	ax, _ := json.Marshal(x)
	by, _ := json.Marshal(y)
	return string(ax) == string(by)
}
