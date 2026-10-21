package ops

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/kryptonn36/multiWorks/internal/db"
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
	return db.New(tx).GetOperationRole(ctx, db.GetOperationRoleParams{ProjectID: projectID, UserID: userID})
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
	exists, err := db.New(tx).SceneExists(ctx, db.SceneExistsParams{SceneID: sceneID, ProjectID: projectID})
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
	exists, err := db.New(tx).ShotExists(ctx, db.ShotExistsParams{ShotID: shotID, SceneID: sceneID, ProjectID: projectID})
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

		_, err = db.New(tx).CreateOperationScene(ctx, db.CreateOperationSceneParams{SceneID: sceneID, ProjectID: projectID, Title: strings.TrimSpace(*p.Title), Description: valueOrEmpty(p.Description), UserID: userID})
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

		_, err = db.New(tx).UpdateOperationScene(ctx, db.UpdateOperationSceneParams{Title: db.NullableString(p.Title), Description: db.NullableString(p.Description), SceneID: sceneID, ProjectID: projectID})
		return err

	case "scene.delete":
		sceneID, err := requireUUID(p.SceneID)
		if err != nil {
			return opError(http.StatusBadRequest, "valid scene_id is required")
		}
		result, err := db.New(tx).DeleteOperationScene(ctx, db.DeleteOperationSceneParams{SceneID: sceneID, ProjectID: projectID})
		if err != nil {
			return err
		}
		n := result
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

		_, err = db.New(tx).CreateOperationShot(ctx, db.CreateOperationShotParams{ShotID: shotID, SceneID: sceneID, Prompt: valueOrEmpty(p.Prompt), Style: valueOrEmpty(p.Style), Notes: valueOrEmpty(p.Notes), ImageUrl: db.NullableString(p.ImageURL), UserID: userID})
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
			_, err = db.New(tx).UpdateOperationShotWithImage(ctx, db.UpdateOperationShotWithImageParams{Prompt: db.NullableString(p.Prompt), Style: db.NullableString(p.Style), Notes: db.NullableString(p.Notes), ImageUrl: db.NullableString(p.ImageURL), ShotID: shotID, SceneID: sceneID, ProjectID: projectID})
		} else {
			_, err = db.New(tx).UpdateOperationShot(ctx, db.UpdateOperationShotParams{Prompt: db.NullableString(p.Prompt), Style: db.NullableString(p.Style), Notes: db.NullableString(p.Notes), ShotID: shotID, SceneID: sceneID, ProjectID: projectID})
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
		result, err := db.New(tx).DeleteOperationShot(ctx, db.DeleteOperationShotParams{ShotID: shotID, SceneID: sceneID, ProjectID: projectID})
		if err != nil {
			return err
		}
		n := result
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
		countProjectScenesResult, countProjectScenesErr := db.New(tx).CountProjectScenes(ctx, projectID)
		count = int(countProjectScenesResult)
		if err := countProjectScenesErr; err != nil {
			return err
		}
		if count != len(ids) {
			return opError(http.StatusConflict, "scene order must include every scene exactly once")
		}
		for pos, id := range ids {
			result, err := db.New(tx).ReorderOperationScene(ctx, db.ReorderOperationSceneParams{Position: int64(pos), ID: id, ProjectID: projectID})
			if err != nil {
				return err
			}
			n := result
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
		countSceneShotsResult, countSceneShotsErr := db.New(tx).CountSceneShots(ctx, sceneID)
		count = int(countSceneShotsResult)
		if err := countSceneShotsErr; err != nil {
			return err
		}
		if count != len(ids) {
			return opError(http.StatusConflict, "shot order must include every shot exactly once")
		}
		for pos, id := range ids {
			result, err := db.New(tx).ReorderOperationShot(ctx, db.ReorderOperationShotParams{Position: int64(pos), ID: id, SceneID: sceneID, ProjectID: projectID})
			if err != nil {
				return err
			}
			n := result
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
