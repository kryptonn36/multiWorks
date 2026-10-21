package storyboard

import (
	"database/sql"
	"errors"
	"github.com/kryptonn36/multiWorks/internal/db"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type Handler struct {
	database *sql.DB
}

func NewHandler(database *sql.DB) *Handler {
	return &Handler{database: database}
}

type Scene struct {
	ID          uuid.UUID `json:"id"`
	ProjectID   uuid.UUID `json:"project_id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Position    int       `json:"position"`
	CreatedBy   uuid.UUID `json:"created_by"`
	CreatedAt   string    `json:"created_at"`
	UpdatedAt   string    `json:"updated_at"`
	Shots       []Shot    `json:"shots"`
}

type Shot struct {
	ID        uuid.UUID `json:"id"`
	SceneID   uuid.UUID `json:"scene_id"`
	Prompt    string    `json:"prompt"`
	Style     string    `json:"style"`
	Notes     string    `json:"notes"`
	ImageURL  *string   `json:"image_url"`
	Position  int       `json:"position"`
	CreatedBy uuid.UUID `json:"created_by"`
	CreatedAt string    `json:"created_at"`
	UpdatedAt string    `json:"updated_at"`
}

type createSceneRequest struct {
	Title       string `json:"title" binding:"required"`
	Description string `json:"description"`
}

type updateSceneRequest struct {
	Title       *string `json:"title"`
	Description *string `json:"description"`
	Position    *int    `json:"position"`
}

type createShotRequest struct {
	Prompt   string  `json:"prompt"`
	Style    string  `json:"style"`
	Notes    string  `json:"notes"`
	ImageURL *string `json:"image_url"`
}

type updateShotRequest struct {
	Prompt   *string `json:"prompt"`
	Style    *string `json:"style"`
	Notes    *string `json:"notes"`
	ImageURL *string `json:"image_url"`
	Position *int    `json:"position"`
}

type reorderRequest struct {
	IDs []string `json:"ids" binding:"required,min=1"`
}

func respondError(c *gin.Context, status int, message string) {
	c.JSON(status, gin.H{"error": message})
}

func parseUUID(value string) (uuid.UUID, bool) {
	id, err := uuid.Parse(value)
	return id, err == nil
}

func currentUserID(c *gin.Context) (uuid.UUID, bool) {
	value, exists := c.Get("userID")
	if !exists {
		return uuid.Nil, false
	}

	id, ok := value.(uuid.UUID)
	return id, ok && id != uuid.Nil
}

// projectRole verifies both project membership and workspace membership.
// An inaccessible project is deliberately reported as not found.
func (h *Handler) projectRole(
	c *gin.Context,
	projectID, userID uuid.UUID,
) (string, error) {
	return db.New(h.database).GetStoryboardRole(c.Request.Context(), db.GetStoryboardRoleParams{ProjectID: projectID, UserID: userID})
}

func (h *Handler) authorize(
	c *gin.Context,
	projectID, userID uuid.UUID,
	write bool,
) bool {
	role, err := h.projectRole(c, projectID, userID)

	if errors.Is(err, sql.ErrNoRows) {
		respondError(c, http.StatusNotFound, "project not found")
		return false
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "could not check project access")
		return false
	}

	if write && role != "owner" && role != "editor" {
		respondError(c, http.StatusForbidden, "editor or owner role required")
		return false
	}

	return true
}

func validText(value string, max int) bool {
	value = strings.TrimSpace(value)
	return len(value) > 0 && len(value) <= max
}

func validOptionalText(value *string, max int) bool {
	return value == nil || len(*value) <= max
}

func validPosition(value *int) bool {
	return value == nil || *value >= 0
}

// GET /api/projects/:projectID/board
func (h *Handler) GetBoard(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		respondError(c, 401, "authentication required")
		return
	}
	projectID, ok := parseUUID(c.Param("projectID"))
	if !ok {
		respondError(c, 404, "project not found")
		return
	}
	if !h.authorize(c, projectID, userID, false) {
		return
	}
	tx, err := h.database.BeginTx(c.Request.Context(), &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		respondError(c, 500, "could not start board snapshot")
		return
	}
	defer tx.Rollback()
	queries := db.New(h.database).WithTx(tx)
	lastSeq, err := queries.GetBoardSequence(c.Request.Context(), projectID)
	if err != nil {
		respondError(c, 500, "could not load board sequence")
		return
	}
	sceneRows, err := queries.ListBoardScenes(c.Request.Context(), db.ListBoardScenesParams{ProjectID: projectID, UserID: userID})
	if err != nil {
		respondError(c, 500, "could not load scenes")
		return
	}
	scenes := make([]Scene, 0, len(sceneRows))
	indexes := make(map[uuid.UUID]int)
	for _, row := range sceneRows {
		indexes[row.ID] = len(scenes)
		scenes = append(scenes, Scene{ID: row.ID, ProjectID: row.ProjectID, Title: row.Title, Description: row.Description, Position: int(row.Position), CreatedBy: row.CreatedBy, CreatedAt: row.SCreatedAt, UpdatedAt: row.SUpdatedAt, Shots: []Shot{}})
	}
	shotRows, err := queries.ListBoardShots(c.Request.Context(), db.ListBoardShotsParams{ProjectID: projectID, UserID: userID})
	if err != nil {
		respondError(c, 500, "could not load shots")
		return
	}
	for _, row := range shotRows {
		if index, ok := indexes[row.SceneID]; ok {
			scenes[index].Shots = append(scenes[index].Shots, Shot{ID: row.ID, SceneID: row.SceneID, Prompt: row.Prompt, Style: row.Style, Notes: row.Notes, ImageURL: db.StringPointer(row.ImageUrl), Position: int(row.Position), CreatedBy: row.CreatedBy, CreatedAt: row.SCreatedAt, UpdatedAt: row.SUpdatedAt})
		}
	}
	if err := tx.Commit(); err != nil {
		respondError(c, 500, "could not finish board snapshot")
		return
	}
	c.JSON(200, gin.H{"project_id": projectID, "last_seq": lastSeq, "scenes": scenes})
}

func (h *Handler) CreateScene(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "authentication required")
		return
	}

	projectID, ok := parseUUID(c.Param("projectID"))
	if !ok {
		respondError(c, http.StatusNotFound, "project not found")
		return
	}

	var req createSceneRequest
	if err := c.ShouldBindJSON(&req); err != nil ||
		!validText(req.Title, 200) ||
		len(req.Description) > 5000 {
		respondError(c, http.StatusBadRequest, "valid title and description are required")
		return
	}

	if !h.authorize(c, projectID, userID, true) {
		return
	}

	id := uuid.New()

	var scene Scene
	createStoryboardSceneResult, err := db.New(h.database).CreateStoryboardScene(c.Request.Context(), db.CreateStoryboardSceneParams{ID: id, ProjectID: projectID, Title: strings.TrimSpace(req.Title), Description: req.Description, UserID: userID})
	scene.ID = createStoryboardSceneResult.ID
	scene.ProjectID = createStoryboardSceneResult.ProjectID
	scene.Title = createStoryboardSceneResult.Title
	scene.Description = createStoryboardSceneResult.Description
	scene.Position = int(createStoryboardSceneResult.Position)
	scene.CreatedBy = createStoryboardSceneResult.CreatedBy
	scene.CreatedAt = createStoryboardSceneResult.CreatedAt
	scene.UpdatedAt = createStoryboardSceneResult.UpdatedAt

	if errors.Is(err, sql.ErrNoRows) {
		respondError(c, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "could not create scene")
		return
	}

	scene.Shots = make([]Shot, 0)
	c.JSON(http.StatusCreated, scene)
}

// PATCH /api/projects/:projectID/scenes/:sceneID
func (h *Handler) UpdateScene(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "authentication required")
		return
	}

	projectID, ok := parseUUID(c.Param("projectID"))
	if !ok {
		respondError(c, http.StatusNotFound, "scene not found")
		return
	}
	sceneID, ok := parseUUID(c.Param("sceneID"))
	if !ok {
		respondError(c, http.StatusNotFound, "scene not found")
		return
	}

	var req updateSceneRequest
	if err := c.ShouldBindJSON(&req); err != nil ||
		(req.Title == nil && req.Description == nil && req.Position == nil) ||
		(req.Title != nil && !validText(*req.Title, 200)) ||
		(req.Description != nil && len(*req.Description) > 5000) ||
		!validPosition(req.Position) {
		respondError(c, http.StatusBadRequest, "invalid scene update")
		return
	}

	if !h.authorize(c, projectID, userID, true) {
		return
	}

	var scene Scene
	updateStoryboardSceneResult, err := db.New(h.database).UpdateStoryboardScene(c.Request.Context(), db.UpdateStoryboardSceneParams{Title: db.NullableString(req.Title), Description: db.NullableString(req.Description), Position: db.NullablePosition(req.Position), SceneID: sceneID, ProjectID: projectID, UserID: userID})
	scene.ID = updateStoryboardSceneResult.ID
	scene.ProjectID = updateStoryboardSceneResult.ProjectID
	scene.Title = updateStoryboardSceneResult.Title
	scene.Description = updateStoryboardSceneResult.Description
	scene.Position = int(updateStoryboardSceneResult.Position)
	scene.CreatedBy = updateStoryboardSceneResult.CreatedBy
	scene.CreatedAt = updateStoryboardSceneResult.SCreatedAt
	scene.UpdatedAt = updateStoryboardSceneResult.SUpdatedAt

	if errors.Is(err, sql.ErrNoRows) {
		respondError(c, http.StatusNotFound, "scene not found")
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "could not update scene")
		return
	}

	scene.Shots = make([]Shot, 0)
	c.JSON(http.StatusOK, scene)
}

// DELETE /api/projects/:projectID/scenes/:sceneID
func (h *Handler) DeleteScene(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "authentication required")
		return
	}

	projectID, ok := parseUUID(c.Param("projectID"))
	if !ok {
		respondError(c, http.StatusNotFound, "scene not found")
		return
	}
	sceneID, ok := parseUUID(c.Param("sceneID"))
	if !ok {
		respondError(c, http.StatusNotFound, "scene not found")
		return
	}

	if !h.authorize(c, projectID, userID, true) {
		return
	}

	result, err := db.New(h.database).DeleteStoryboardScene(c.Request.Context(), db.DeleteStoryboardSceneParams{SceneID: sceneID, ProjectID: projectID, UserID: userID})
	if err != nil {
		respondError(c, http.StatusInternalServerError, "could not delete scene")
		return
	}

	count := result
	if err != nil {
		respondError(c, http.StatusInternalServerError, "could not confirm scene deletion")
		return
	}
	if count == 0 {
		respondError(c, http.StatusNotFound, "scene not found")
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "scene deleted"})
}

// POST /api/projects/:projectID/scenes/:sceneID/shots
func (h *Handler) CreateShot(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "authentication required")
		return
	}

	projectID, ok := parseUUID(c.Param("projectID"))
	if !ok {
		respondError(c, http.StatusNotFound, "scene not found")
		return
	}
	sceneID, ok := parseUUID(c.Param("sceneID"))
	if !ok {
		respondError(c, http.StatusNotFound, "scene not found")
		return
	}

	var req createShotRequest
	if err := c.ShouldBindJSON(&req); err != nil ||
		len(req.Prompt) > 10000 ||
		len(req.Style) > 500 ||
		len(req.Notes) > 5000 {
		respondError(c, http.StatusBadRequest, "invalid shot fields")
		return
	}

	if !h.authorize(c, projectID, userID, true) {
		return
	}

	id := uuid.New()
	var shot Shot
	createStoryboardShotResult, err := db.New(h.database).CreateStoryboardShot(c.Request.Context(), db.CreateStoryboardShotParams{ID: id, Prompt: req.Prompt, Style: req.Style, Notes: req.Notes, ImageUrl: db.NullableString(req.ImageURL), UserID: userID, SceneID: sceneID, ProjectID: projectID})
	shot.ID = createStoryboardShotResult.ID
	shot.SceneID = createStoryboardShotResult.SceneID
	shot.Prompt = createStoryboardShotResult.Prompt
	shot.Style = createStoryboardShotResult.Style
	shot.Notes = createStoryboardShotResult.Notes
	shot.ImageURL = db.StringPointer(createStoryboardShotResult.ImageUrl)
	shot.Position = int(createStoryboardShotResult.Position)
	shot.CreatedBy = createStoryboardShotResult.CreatedBy
	shot.CreatedAt = createStoryboardShotResult.CreatedAt
	shot.UpdatedAt = createStoryboardShotResult.UpdatedAt

	if errors.Is(err, sql.ErrNoRows) {
		respondError(c, http.StatusNotFound, "scene not found")
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "could not create shot")
		return
	}

	c.JSON(http.StatusCreated, shot)
}

// PATCH /api/projects/:projectID/scenes/:sceneID/shots/:shotID
func (h *Handler) UpdateShot(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "authentication required")
		return
	}

	projectID, ok := parseUUID(c.Param("projectID"))
	if !ok {
		respondError(c, http.StatusNotFound, "shot not found")
		return
	}
	sceneID, ok := parseUUID(c.Param("sceneID"))
	if !ok {
		respondError(c, http.StatusNotFound, "shot not found")
		return
	}
	shotID, ok := parseUUID(c.Param("shotID"))
	if !ok {
		respondError(c, http.StatusNotFound, "shot not found")
		return
	}

	var req updateShotRequest
	if err := c.ShouldBindJSON(&req); err != nil ||
		(req.Prompt == nil && req.Style == nil && req.Notes == nil &&
			req.ImageURL == nil && req.Position == nil) ||
		!validOptionalText(req.Prompt, 10000) ||
		!validOptionalText(req.Style, 500) ||
		!validOptionalText(req.Notes, 5000) ||
		!validPosition(req.Position) {
		respondError(c, http.StatusBadRequest, "invalid shot update")
		return
	}

	if !h.authorize(c, projectID, userID, true) {
		return
	}

	var shot Shot
	updateStoryboardShotResult, err := db.New(h.database).UpdateStoryboardShot(c.Request.Context(), db.UpdateStoryboardShotParams{Prompt: db.NullableString(req.Prompt), Style: db.NullableString(req.Style), Notes: db.NullableString(req.Notes), ImageUrl: db.NullableString(req.ImageURL), Position: db.NullablePosition(req.Position), ShotID: shotID, SceneID: sceneID, ProjectID: projectID, UserID: userID})
	shot.ID = updateStoryboardShotResult.ID
	shot.SceneID = updateStoryboardShotResult.SceneID
	shot.Prompt = updateStoryboardShotResult.Prompt
	shot.Style = updateStoryboardShotResult.Style
	shot.Notes = updateStoryboardShotResult.Notes
	shot.ImageURL = db.StringPointer(updateStoryboardShotResult.ImageUrl)
	shot.Position = int(updateStoryboardShotResult.Position)
	shot.CreatedBy = updateStoryboardShotResult.CreatedBy
	shot.CreatedAt = updateStoryboardShotResult.SCreatedAt
	shot.UpdatedAt = updateStoryboardShotResult.SUpdatedAt

	if errors.Is(err, sql.ErrNoRows) {
		respondError(c, http.StatusNotFound, "shot not found")
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "could not update shot")
		return
	}

	c.JSON(http.StatusOK, shot)
}

// DELETE /api/projects/:projectID/scenes/:sceneID/shots/:shotID
func (h *Handler) DeleteShot(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "authentication required")
		return
	}

	projectID, ok := parseUUID(c.Param("projectID"))
	if !ok {
		respondError(c, http.StatusNotFound, "shot not found")
		return
	}
	sceneID, ok := parseUUID(c.Param("sceneID"))
	if !ok {
		respondError(c, http.StatusNotFound, "shot not found")
		return
	}
	shotID, ok := parseUUID(c.Param("shotID"))
	if !ok {
		respondError(c, http.StatusNotFound, "shot not found")
		return
	}

	if !h.authorize(c, projectID, userID, true) {
		return
	}

	result, err := db.New(h.database).DeleteStoryboardShot(c.Request.Context(), db.DeleteStoryboardShotParams{ShotID: shotID, SceneID: sceneID, ProjectID: projectID, UserID: userID})
	if err != nil {
		respondError(c, http.StatusInternalServerError, "could not delete shot")
		return
	}

	count := result
	if err != nil {
		respondError(c, http.StatusInternalServerError, "could not confirm shot deletion")
		return
	}
	if count == 0 {
		respondError(c, http.StatusNotFound, "shot not found")
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "shot deleted"})
}

// PUT /api/projects/:projectID/scenes/order
func (h *Handler) ReorderScenes(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "authentication required")
		return
	}

	projectID, ok := parseUUID(c.Param("projectID"))
	if !ok {
		respondError(c, http.StatusNotFound, "project not found")
		return
	}

	var req reorderRequest
	if err := c.ShouldBindJSON(&req); err != nil || len(req.IDs) > 500 {
		respondError(c, http.StatusBadRequest, "ids must contain at most 500 scene IDs")
		return
	}

	ids, ok := parseUniqueIDs(req.IDs)
	if !ok {
		respondError(c, http.StatusBadRequest, "scene IDs must be valid and unique")
		return
	}

	if !h.authorize(c, projectID, userID, true) {
		return
	}

	tx, err := h.database.BeginTx(c.Request.Context(), nil)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "could not reorder scenes")
		return
	}
	defer tx.Rollback()

	var count int
	countProjectScenesResult, err := db.New(tx).CountProjectScenes(c.Request.Context(), projectID)
	count = int(countProjectScenesResult)
	if err != nil || count != len(ids) {
		respondError(c, http.StatusBadRequest, "provide every scene in this project exactly once")
		return
	}

	for position, id := range ids {
		result, err := db.New(tx).ReorderStoryboardScene(c.Request.Context(), db.ReorderStoryboardSceneParams{Position: int64(position), ID: id, ProjectID: projectID})
		if err != nil {
			respondError(c, http.StatusInternalServerError, "could not reorder scenes")
			return
		}
		changed := result
		if err != nil || changed != 1 {
			respondError(c, http.StatusBadRequest, "scene does not belong to this project")
			return
		}
	}

	if err := tx.Commit(); err != nil {
		respondError(c, http.StatusInternalServerError, "could not save scene order")
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "scenes reordered"})
}

// PUT /api/projects/:projectID/scenes/:sceneID/shots/order
func (h *Handler) ReorderShots(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "authentication required")
		return
	}

	projectID, ok := parseUUID(c.Param("projectID"))
	if !ok {
		respondError(c, http.StatusNotFound, "project not found")
		return
	}
	sceneID, ok := parseUUID(c.Param("sceneID"))
	if !ok {
		respondError(c, http.StatusNotFound, "scene not found")
		return
	}

	var req reorderRequest
	if err := c.ShouldBindJSON(&req); err != nil || len(req.IDs) > 1000 {
		respondError(c, http.StatusBadRequest, "invalid shot order")
		return
	}

	ids, ok := parseUniqueIDs(req.IDs)
	if !ok {
		respondError(c, http.StatusBadRequest, "shot IDs must be valid and unique")
		return
	}

	if !h.authorize(c, projectID, userID, true) {
		return
	}

	tx, err := h.database.BeginTx(c.Request.Context(), nil)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "could not reorder shots")
		return
	}
	defer tx.Rollback()

	var count int
	countSceneShotsResult, err := db.New(tx).CountSceneShots(c.Request.Context(), sceneID)
	count = int(countSceneShotsResult)
	if err != nil || count != len(ids) {
		respondError(c, http.StatusBadRequest, "provide every shot in this scene exactly once")
		return
	}

	for position, id := range ids {
		result, err := db.New(tx).ReorderStoryboardShot(c.Request.Context(), db.ReorderStoryboardShotParams{Position: int64(position), ID: id, SceneID: sceneID, ProjectID: projectID})
		if err != nil {
			respondError(c, http.StatusInternalServerError, "could not reorder shots")
			return
		}
		changed := result
		if err != nil || changed != 1 {
			respondError(c, http.StatusBadRequest, "shot does not belong to this scene")
			return
		}
	}

	if err := tx.Commit(); err != nil {
		respondError(c, http.StatusInternalServerError, "could not save shot order")
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "shots reordered"})
}

func parseUniqueIDs(values []string) ([]uuid.UUID, bool) {
	ids := make([]uuid.UUID, 0, len(values))
	seen := make(map[uuid.UUID]struct{}, len(values))

	for _, value := range values {
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil {
			return nil, false
		}
		if _, exists := seen[id]; exists {
			return nil, false
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}

	return ids, true
}
