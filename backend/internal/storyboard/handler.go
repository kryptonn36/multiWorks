
package storyboard

import (
	"database/sql"
	"errors"
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
	var role string

	err := h.database.QueryRowContext(
		c.Request.Context(),
		`
		SELECT pm.role
		FROM project_members pm
		JOIN projects p ON p.id = pm.project_id
		JOIN workspace_members wm
		  ON wm.workspace_id = p.workspace_id
		 AND wm.user_id = pm.user_id
		WHERE p.id = $1
		  AND pm.user_id = $2
		`,
		projectID,
		userID,
	).Scan(&role)

	return role, err
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
		respondError(c, http.StatusUnauthorized, "authentication required")
		return
	}

	projectID, ok := parseUUID(c.Param("projectID"))
	if !ok {
		respondError(c, http.StatusNotFound, "project not found")
		return
	}

	if !h.authorize(c, projectID, userID, false) {
		return
	}

	rows, err := h.database.QueryContext(
		c.Request.Context(),
		`
		SELECT s.id, s.project_id, s.title, s.description, s.position,
			s.created_by, s.created_at::text, s.updated_at::text
		FROM scenes s
		WHERE s.project_id = $1
		AND EXISTS (
			SELECT 1
			FROM project_members pm
			JOIN projects p ON p.id = pm.project_id
			JOIN workspace_members wm
			ON wm.workspace_id = p.workspace_id
			AND wm.user_id = pm.user_id
			WHERE pm.project_id = $1
			AND pm.user_id = $2
			AND wm.user_id = $2
		)
		ORDER BY s.position, s.created_at, s.id
		`,
		projectID, userID,
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "could not load scenes")
		return
	}
	defer rows.Close()

	scenes := make([]Scene, 0)
	sceneIndexes := make(map[uuid.UUID]int)

	for rows.Next() {
		var scene Scene
		var createdAt, updatedAt sql.NullString

		err := rows.Scan(
			&scene.ID,
			&scene.ProjectID,
			&scene.Title,
			&scene.Description,
			&scene.Position,
			&scene.CreatedBy,
			&createdAt,
			&updatedAt,
		)
		if err != nil {
			respondError(c, http.StatusInternalServerError, "could not read scenes")
			return
		}

		scene.CreatedAt = createdAt.String
		scene.UpdatedAt = updatedAt.String
		scene.Shots = make([]Shot, 0)
		sceneIndexes[scene.ID] = len(scenes)
		scenes = append(scenes, scene)
	}
	if err := rows.Err(); err != nil {
		respondError(c, http.StatusInternalServerError, "could not read scenes")
		return
	}

	shotRows, err := h.database.QueryContext(
		c.Request.Context(),
		`
		SELECT s.id, s.scene_id, s.prompt, s.style, s.notes,
			s.image_url, s.position, s.created_by,
			s.created_at::text, s.updated_at::text
		FROM shots s
		JOIN scenes sc ON sc.id = s.scene_id
		WHERE sc.project_id = $1
		AND EXISTS (
			SELECT 1
			FROM project_members pm
			JOIN projects p ON p.id = pm.project_id
			JOIN workspace_members wm
			ON wm.workspace_id = p.workspace_id
			AND wm.user_id = pm.user_id
			WHERE pm.project_id = $1
			AND pm.user_id = $2
			AND wm.user_id = $2
		)
		ORDER BY s.scene_id, s.position, s.created_at, s.id
		`,
		projectID, userID,
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "could not load shots")
		return
	}
	defer shotRows.Close()

	for shotRows.Next() {
		var shot Shot
		var createdAt, updatedAt sql.NullString

		err := shotRows.Scan(
			&shot.ID,
			&shot.SceneID,
			&shot.Prompt,
			&shot.Style,
			&shot.Notes,
			&shot.ImageURL,
			&shot.Position,
			&shot.CreatedBy,
			&createdAt,
			&updatedAt,
		)
		if err != nil {
			respondError(c, http.StatusInternalServerError, "could not read shots")
			return
		}

		shot.CreatedAt = createdAt.String
		shot.UpdatedAt = updatedAt.String

		if index, exists := sceneIndexes[shot.SceneID]; exists {
			scenes[index].Shots = append(scenes[index].Shots, shot)
		}
	}
	if err := shotRows.Err(); err != nil {
		respondError(c, http.StatusInternalServerError, "could not read shots")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"project_id": projectID,
		"scenes":     scenes,
	})
}

// POST /api/projects/:projectID/scenes
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
	err := h.database.QueryRowContext(
		c.Request.Context(),
		`
		INSERT INTO scenes (
		    id, project_id, title, description, position, created_by
		)
		SELECT $1, $2, $3, $4,
		       COALESCE((SELECT MAX(position) + 1
		                 FROM scenes WHERE project_id = $2), 0),
		       $5
		WHERE EXISTS (
		    SELECT 1
		    FROM project_members pm
		    JOIN projects p ON p.id = pm.project_id
		    JOIN workspace_members wm
		      ON wm.workspace_id = p.workspace_id
		     AND wm.user_id = pm.user_id
		    WHERE pm.project_id = $2
		      AND pm.user_id = $5
		      AND pm.role IN ('owner', 'editor')
		)
		RETURNING id, project_id, title, description, position,
		          created_by, created_at::text, updated_at::text
		`,
		id, projectID, strings.TrimSpace(req.Title),
		req.Description, userID,
	).Scan(
		&scene.ID, &scene.ProjectID, &scene.Title, &scene.Description,
		&scene.Position, &scene.CreatedBy, &scene.CreatedAt, &scene.UpdatedAt,
	)

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
	err := h.database.QueryRowContext(
		c.Request.Context(),
		`
		UPDATE scenes s
		SET position = $1, updated_at = NOW()
		WHERE s.id = $2
		AND s.project_id = $3
		AND EXISTS (
			SELECT 1
			FROM project_members pm
			JOIN projects p ON p.id = pm.project_id
			JOIN workspace_members wm
			ON wm.workspace_id = p.workspace_id
			AND wm.user_id = pm.user_id
			WHERE pm.project_id = $3
			AND pm.user_id = $4
			AND pm.role IN ('owner', 'editor')
		)
		RETURNING s.id, s.project_id, s.title, s.description, s.position,
		          s.created_by, s.created_at::text, s.updated_at::text
		`,
		sceneID, projectID, req.Title, req.Description, req.Position, userID,
	).Scan(
		&scene.ID, &scene.ProjectID, &scene.Title, &scene.Description,
		&scene.Position, &scene.CreatedBy, &scene.CreatedAt, &scene.UpdatedAt,
	)

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

	result, err := h.database.ExecContext(
		c.Request.Context(),
		`
		DELETE FROM scenes s
		WHERE s.id = $1 AND s.project_id = $2
		  AND EXISTS (
		    SELECT 1 FROM project_members pm
		    JOIN projects p ON p.id = pm.project_id
		    JOIN workspace_members wm
		      ON wm.workspace_id = p.workspace_id
		     AND wm.user_id = pm.user_id
		    WHERE pm.project_id = $2 AND pm.user_id = $3
		      AND pm.role IN ('owner', 'editor')
		  )
		`,
		sceneID, projectID, userID,
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "could not delete scene")
		return
	}

	count, err := result.RowsAffected()
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
	err := h.database.QueryRowContext(
		c.Request.Context(),
		`
		INSERT INTO shots (
		    id, scene_id, prompt, style, notes, image_url, position, created_by
		)
		SELECT $1, sc.id, $3, $4, $5, $6,
		       COALESCE((SELECT MAX(position) + 1
		                 FROM shots WHERE scene_id = sc.id), 0),
		       $7
		FROM scenes sc
		WHERE sc.id = $2 AND sc.project_id = $8
		  AND EXISTS (
		    SELECT 1 FROM project_members pm
		    JOIN projects p ON p.id = pm.project_id
		    JOIN workspace_members wm
		      ON wm.workspace_id = p.workspace_id
		     AND wm.user_id = pm.user_id
		    WHERE pm.project_id = $8 AND pm.user_id = $7
		      AND pm.role IN ('owner', 'editor')
		  )
		RETURNING id, scene_id, prompt, style, notes, image_url, position,
		          created_by, created_at::text, updated_at::text
		`,
		id, sceneID, req.Prompt, req.Style, req.Notes, req.ImageURL,
		userID, projectID,
	).Scan(
		&shot.ID, &shot.SceneID, &shot.Prompt, &shot.Style, &shot.Notes,
		&shot.ImageURL, &shot.Position, &shot.CreatedBy,
		&shot.CreatedAt, &shot.UpdatedAt,
	)

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
	err := h.database.QueryRowContext(
		c.Request.Context(),
		`
		UPDATE shots s
		SET position = $1, updated_at = NOW()
		FROM scenes sc
		WHERE s.id = $2
		AND s.scene_id = $3
		AND sc.id = s.scene_id
		AND sc.project_id = $4
		AND EXISTS (
			SELECT 1
			FROM project_members pm
			JOIN projects p ON p.id = pm.project_id
			JOIN workspace_members wm
			ON wm.workspace_id = p.workspace_id
			AND wm.user_id = pm.user_id
			WHERE pm.project_id = $4
			AND pm.user_id = $5
			AND pm.role IN ('owner', 'editor')
		)
		RETURNING s.id, s.scene_id, s.prompt, s.style, s.notes,
		          s.image_url, s.position, s.created_by,
		          s.created_at::text, s.updated_at::text
		`,
		shotID, sceneID, projectID, req.Prompt, req.Style, req.Notes,
		req.ImageURL, req.Position, userID,
	).Scan(
		&shot.ID, &shot.SceneID, &shot.Prompt, &shot.Style, &shot.Notes,
		&shot.ImageURL, &shot.Position, &shot.CreatedBy,
		&shot.CreatedAt, &shot.UpdatedAt,
	)

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

	result, err := h.database.ExecContext(
		c.Request.Context(),
		`
		DELETE FROM shots s
		USING scenes sc
		WHERE s.id = $1 AND s.scene_id = $2
		  AND sc.id = s.scene_id AND sc.project_id = $3
		  AND EXISTS (
		    SELECT 1 FROM project_members pm
		    JOIN projects p ON p.id = pm.project_id
		    JOIN workspace_members wm
		      ON wm.workspace_id = p.workspace_id
		     AND wm.user_id = pm.user_id
		    WHERE pm.project_id = $3 AND pm.user_id = $4
		      AND pm.role IN ('owner', 'editor')
		  )
		`,
		shotID, sceneID, projectID, userID,
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "could not delete shot")
		return
	}

	count, err := result.RowsAffected()
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
	err = tx.QueryRowContext(
		c.Request.Context(),
		`SELECT COUNT(*) FROM scenes WHERE project_id = $1`,
		projectID,
	).Scan(&count)
	if err != nil || count != len(ids) {
		respondError(c, http.StatusBadRequest, "provide every scene in this project exactly once")
		return
	}

	for position, id := range ids {
		result, err := tx.ExecContext(
			c.Request.Context(),
			`UPDATE scenes SET position = $1, updated_at = NOW()
			 WHERE id = $2 AND project_id = $3`,
			position, id, projectID,
		)
		if err != nil {
			respondError(c, http.StatusInternalServerError, "could not reorder scenes")
			return
		}
		changed, err := result.RowsAffected()
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
	err = tx.QueryRowContext(
		c.Request.Context(),
		`SELECT COUNT(*) FROM shots WHERE scene_id = $1`,
		sceneID,
	).Scan(&count)
	if err != nil || count != len(ids) {
		respondError(c, http.StatusBadRequest, "provide every shot in this scene exactly once")
		return
	}

	for position, id := range ids {
		result, err := tx.ExecContext(
			c.Request.Context(),
			`UPDATE shots s SET position = $1, updated_at = NOW()
			 FROM scenes sc
			 WHERE s.id = $2 AND s.scene_id = $3
			   AND sc.id = s.scene_id AND sc.project_id = $4`,
			position, id, sceneID, projectID,
		)
		if err != nil {
			respondError(c, http.StatusInternalServerError, "could not reorder shots")
			return
		}
		changed, err := result.RowsAffected()
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
