package workspace

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

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

type createWorkspaceRequest struct {
	Name string `json:"name" binding:"required"`
}

type addWorkspaceMemberRequest struct {
    Email string `json:"email" binding:"required,email"`
    Role  string `json:"role" binding:"required"`
}

type workspaceResponse struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	CreatedBy uuid.UUID `json:"created_by"`
	CreatedAt interface{} `json:"created_at"`
}

type workspaceMemberResponse struct {
	UserID   uuid.UUID `json:"user_id"`
	Email    string    `json:"email"`
	Role     string    `json:"role"`
	JoinedAt interface{} `json:"joined_at"`
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

	var req createWorkspaceRequest
	if err := c.ShouldBindJSON(&req); err != nil || !validName(req.Name) {
		respondError(c, http.StatusBadRequest, "name must be 1 to 100 characters")
		return
	}
	name := strings.TrimSpace(req.Name)

	tx, err := h.database.BeginTx(c.Request.Context(), nil)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "could not create workspace")
		return
	}
	defer func() { _ = tx.Rollback() }()

	queries := db.New(tx)
	workspace, err := queries.CreateWorkspace(
		c.Request.Context(),
		db.CreateWorkspaceParams{
			ID:        uuid.New(),
			Name:      name,
			CreatedBy: userID,
		},
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "could not create workspace")
		return
	}

	err = queries.AddWorkspaceMember(
		c.Request.Context(),
		db.AddWorkspaceMemberParams{
			WorkspaceID: workspace.ID,
			UserID:      userID,
			Role:        "owner",
		},
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "could not assign workspace owner")
		return
	}

	if err := tx.Commit(); err != nil {
		respondError(c, http.StatusInternalServerError, "could not create workspace")
		return
	}

	c.JSON(http.StatusCreated, workspaceResponse{
		ID:        workspace.ID,
		Name:      workspace.Name,
		CreatedBy: workspace.CreatedBy,
		CreatedAt: workspace.CreatedAt,
	})
}

func (h *Handler) List(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "authentication required")
		return
	}

	workspaces, err := h.queries.ListWorkspaces(c.Request.Context(), userID)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "could not list workspaces")
		return
	}

	result := make([]workspaceResponse, 0, len(workspaces))
	for _, w := range workspaces {
		result = append(result, workspaceResponse{
			ID:        w.ID,
			Name:      w.Name,
			CreatedBy: w.CreatedBy,
			CreatedAt: w.CreatedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"workspaces": result})
}

func (h *Handler) Get(c *gin.Context) {
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

	w, err := h.queries.GetWorkspaceByID(
		c.Request.Context(),
		db.GetWorkspaceByIDParams{
			ID:     workspaceID,
			UserID: userID,
		},
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respondError(c, http.StatusNotFound, "workspace not found")
			return
		}
		respondError(c, http.StatusInternalServerError, "could not retrieve workspace")
		return
	}

	c.JSON(http.StatusOK, workspaceResponse{
		ID:        w.ID,
		Name:      w.Name,
		CreatedBy: w.CreatedBy,
		CreatedAt: w.CreatedAt,
	})
}

func (h *Handler) ListMembers(c *gin.Context) {
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
		respondError(c, http.StatusInternalServerError, "could not retrieve members")
		return
	}

	members, err := h.queries.ListWorkspaceMembers(
		c.Request.Context(),
		db.ListWorkspaceMembersParams{
			WorkspaceID: workspaceID,
			UserID:      userID,
		},
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "could not retrieve members")
		return
	}

	result := make([]workspaceMemberResponse, 0, len(members))
	for _, m := range members {
		result = append(result, workspaceMemberResponse{
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

    workspaceID, ok := parseID(c.Param("workspaceID"))
    if !ok {
        respondError(c, http.StatusNotFound, "workspace not found")
        return
    }

    var req addWorkspaceMemberRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        respondError(c, http.StatusBadRequest, "valid email and role are required")
        return
    }

    email := strings.TrimSpace(req.Email)
    role := strings.TrimSpace(req.Role)

    if role != "admin" && role != "member" {
        respondError(c, http.StatusBadRequest, "role must be admin or member")
        return
    }

    // Check whether the current user is allowed to add members.
    actorRole, err := h.queries.GetWorkspaceRole(
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
        respondError(
            c,
            http.StatusInternalServerError,
            "could not check workspace permissions",
        )
        return
    }

    if actorRole != "owner" && actorRole != "admin" {
        respondError(
            c,
            http.StatusForbidden,
            "workspace owner or admin role required",
        )
        return
    }

    // Resolve the teammate's email to their internal UUID.
    target, err := h.queries.GetUserByEmailForWorkspace(
        c.Request.Context(),
        email,
    )

    if errors.Is(err, sql.ErrNoRows) {
        respondError(c, http.StatusNotFound, "user not found")
        return
    }
    if err != nil {
        respondError(
            c,
            http.StatusInternalServerError,
            "could not find user",
        )
        return
    }

    // Use the UUID internally; the client only needs to provide an email.
    err = h.queries.AddWorkspaceMember(
        c.Request.Context(),
        db.AddWorkspaceMemberParams{
            WorkspaceID: workspaceID,
            UserID:      target.ID,
            Role:        role,
        },
    )

    if err != nil {
        respondError(
            c,
            http.StatusInternalServerError,
            "could not add workspace member",
        )
        return
    }

    c.JSON(http.StatusOK, gin.H{
        "status": "member added",
        "email":  target.Email,
        "role":   role,
    })
}
