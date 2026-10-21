package project

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/kryptonn36/multiWorks/internal/db"
)

type Handler struct {
	database *sql.DB
	queries  *db.Queries
}

func NewHandler(database *sql.DB) *Handler {
	return &Handler{
		database: database,
		queries:  db.New(database),
	}
}

type createProjectRequest struct {
	Name string `json:"name" binding:"required"`
}

type addProjectMemberRequest struct {
	Email string `json:"email" binding:"required,email"`
	Role  string `json:"role" binding:"required"`
}

type projectResponse struct {
	ID          uuid.UUID `json:"id"`
	WorkspaceID uuid.UUID `json:"workspace_id"`
	Name        string    `json:"name"`
	CreatedBy   uuid.UUID `json:"created_by"`
	LastSeq     int64     `json:"last_seq"`
	MemberRole  string    `json:"member_role,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type projectMemberResponse struct {
	UserID   uuid.UUID `json:"user_id"`
	Email    string    `json:"email"`
	Role     string    `json:"role"`
	JoinedAt time.Time `json:"joined_at"`
}

func currentUserID(c *gin.Context) (uuid.UUID, bool) {
	value, exists := c.Get("userID")
	if !exists {
		return uuid.Nil, false
	}
	id, ok := value.(uuid.UUID)
	return id, ok && id != uuid.Nil
}

func respondError(c *gin.Context, status int, message string) {
	c.JSON(status, gin.H{"error": message})
}

func parseID(value string) (uuid.UUID, bool) {
	id, err := uuid.Parse(value)
	return id, err == nil && id != uuid.Nil
}

func validName(name string) bool {
	name = strings.TrimSpace(name)
	return len(name) >= 1 && len(name) <= 100
}

func (h *Handler) Create(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "authentication required")
		return
	}

	workspaceID, ok := parseID(c.Param("workspaceID"))
	if !ok {
		respondError(c, http.StatusNotFound, "workspace not found")
		return
	}

	var req createProjectRequest
	if err := c.ShouldBindJSON(&req); err != nil || !validName(req.Name) {
		respondError(c, http.StatusBadRequest, "name must be 1 to 100 characters")
		return
	}
	name := strings.TrimSpace(req.Name)

	// Only workspace members can create projects.
	_, err := h.queries.GetWorkspaceRole(
		c.Request.Context(),
		db.GetWorkspaceRoleParams{
			WorkspaceID: workspaceID,
			UserID:      userID,
		},
	)
	if errors.Is(err, sql.ErrNoRows) {
		respondError(c, http.StatusNotFound, "workspace not found")
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "could not check workspace permissions")
		return
	}

	tx, err := h.database.BeginTx(c.Request.Context(), nil)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "could not create project")
		return
	}
	defer func() { _ = tx.Rollback() }()

	queries := db.New(tx)
	project, err := queries.CreateProject(
		c.Request.Context(),
		db.CreateProjectParams{
			ID:          uuid.New(),
			WorkspaceID: workspaceID,
			Name:        name,
			CreatedBy:   userID,
		},
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "could not create project")
		return
	}

	_, err = queries.AddProjectMember(
		c.Request.Context(),
		db.AddProjectMemberParams{
			ProjectID: project.ID,
			UserID:    userID,
			Role:      "owner",
		},
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "could not assign project owner")
		return
	}

	if err := tx.Commit(); err != nil {
		respondError(c, http.StatusInternalServerError, "could not create project")
		return
	}

	c.JSON(http.StatusCreated, projectResponse{
		ID:          project.ID,
		WorkspaceID: project.WorkspaceID,
		Name:        project.Name,
		CreatedBy:   project.CreatedBy,
		LastSeq:     project.LastSeq,
		MemberRole:  "owner",
		CreatedAt:   project.CreatedAt,
		UpdatedAt:   project.UpdatedAt,
	})
}

func (h *Handler) List(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "authentication required")
		return
	}

	workspaceID, ok := parseID(c.Param("workspaceID"))
	if !ok {
		respondError(c, http.StatusNotFound, "workspace not found")
		return
	}

	projects, err := h.queries.ListProjects(
		c.Request.Context(),
		db.ListProjectsParams{
			WorkspaceID: workspaceID,
			UserID:      userID,
		},
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "could not list projects")
		return
	}

	result := make([]projectResponse, 0, len(projects))
	for _, p := range projects {
		result = append(result, projectResponse{
			ID:          p.ID,
			WorkspaceID: p.WorkspaceID,
			Name:        p.Name,
			CreatedBy:   p.CreatedBy,
			LastSeq:     p.LastSeq,
			MemberRole:  p.MemberRole,
			CreatedAt:   p.CreatedAt,
			UpdatedAt:   p.UpdatedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"projects": result})
}

func (h *Handler) Get(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "authentication required")
		return
	}

	projectID, ok := parseID(c.Param("projectID"))
	if !ok {
		respondError(c, http.StatusNotFound, "project not found")
		return
	}

	p, err := h.queries.GetProjectByID(
		c.Request.Context(),
		db.GetProjectByIDParams{
			ID:     projectID,
			UserID: userID,
		},
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respondError(c, http.StatusNotFound, "project not found")
			return
		}
		respondError(c, http.StatusInternalServerError, "could not retrieve project")
		return
	}

	c.JSON(http.StatusOK, projectResponse{
		ID:          p.ID,
		WorkspaceID: p.WorkspaceID,
		Name:        p.Name,
		CreatedBy:   p.CreatedBy,
		LastSeq:     p.LastSeq,
		MemberRole:  p.MemberRole,
		CreatedAt:   p.CreatedAt,
		UpdatedAt:   p.UpdatedAt,
	})
}

func (h *Handler) ListMembers(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "authentication required")
		return
	}

	projectID, ok := parseID(c.Param("projectID"))
	if !ok {
		respondError(c, http.StatusNotFound, "project not found")
		return
	}

	_, err := h.queries.GetProjectRole(
		c.Request.Context(),
		db.GetProjectRoleParams{
			ProjectID: projectID,
			UserID:    userID,
		},
	)
	if errors.Is(err, sql.ErrNoRows) {
		respondError(c, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "could not check project access")
		return
	}

	members, err := h.queries.ListProjectMembers(
		c.Request.Context(),
		db.ListProjectMembersParams{
			ProjectID: projectID,
			UserID:    userID,
		},
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "could not list project members")
		return
	}

	result := make([]projectMemberResponse, 0, len(members))
	for _, m := range members {
		result = append(result, projectMemberResponse{
			UserID:   m.UserID,
			Email:    m.Email,
			Role:     m.Role,
			JoinedAt: m.JoinedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"members": result})
}

func (h *Handler) AddMember(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "authentication required")
		return
	}

	projectID, ok := parseID(c.Param("projectID"))
	if !ok {
		respondError(c, http.StatusNotFound, "project not found")
		return
	}

	var req addProjectMemberRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "valid email and role are required")
		return
	}

	role := strings.TrimSpace(req.Role)
	if role != "editor" && role != "viewer" {
		respondError(c, http.StatusBadRequest, "role must be editor or viewer")
		return
	}

	actorRole, err := h.queries.GetProjectRole(
		c.Request.Context(),
		db.GetProjectRoleParams{
			ProjectID: projectID,
			UserID:    userID,
		},
	)
	if errors.Is(err, sql.ErrNoRows) {
		respondError(c, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "could not check project permissions")
		return
	}
	if actorRole != "owner" {
		respondError(c, http.StatusForbidden, "project owner role required")
		return
	}

	// Reuse the case-insensitive email lookup used by workspace membership.
	// Membership records continue to reference the user's internal UUID.
	target, err := h.queries.GetUserByEmailForWorkspace(
		c.Request.Context(),
		strings.TrimSpace(req.Email),
	)
	if errors.Is(err, sql.ErrNoRows) {
		respondError(c, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "could not find user")
		return
	}

	_, err = h.queries.AddProjectMember(
		c.Request.Context(),
		db.AddProjectMemberParams{
			ProjectID: projectID,
			UserID:    target.ID,
			Role:      role,
		},
	)
	if errors.Is(err, sql.ErrNoRows) {
		// The target must belong to the project's workspace.
		respondError(c, http.StatusNotFound, "add this user to the project's workspace before adding them to the project")
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "could not add project member")
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "member added"})
}
