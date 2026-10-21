package auth

import (
	"database/sql"
	"errors"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/kryptonn36/multiWorks/internal/db"
)

type Handler struct {
	queries     *db.Queries
	tokenSecret string
	tokenExpiry time.Duration
}

func NewHandler(
	queries *db.Queries,
	tokenSecret string,
	tokenExpiry time.Duration,
) *Handler {
	return &Handler{
		queries:     queries,
		tokenSecret: tokenSecret,
		tokenExpiry: tokenExpiry,
	}
}

type authRequest struct {
	Email    string `json:"email" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type userResponse struct {
	ID        uuid.UUID `json:"id"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
}

type loginResponse struct {
	User        userResponse `json:"user"`
	AccessToken string       `json:"access_token"`
	TokenType   string       `json:"token_type"`
	ExpiresIn   int64        `json:"expires_in"`
}

func respondError(c *gin.Context, status int, message string) {
	c.JSON(status, gin.H{"error": message})
}

func normalizeEmail(email string) (string, bool) {
	email = strings.ToLower(strings.TrimSpace(email))

	if len(email) == 0 || len(email) > 254 {
		return "", false
	}

	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email {
		return "", false
	}

	return email, true
}

func validPassword(password string) bool {
	return utf8.ValidString(password) &&
		len(password) >= 8 &&
		len(password) <= 128 &&
		strings.TrimSpace(password) != ""
}

func (h *Handler) Register(c *gin.Context) {
	var req authRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, 400, "email and password are required")
		return
	}

	email, ok := normalizeEmail(req.Email)
	if !ok {
		respondError(c, 400, "a valid email address is required")
		return
	}
	if !validPassword(req.Password) {
		respondError(c, 400, "password must be 8 to 128 bytes and not blank")
		return
	}

	passwordHash, err := HashPassword(req.Password)
	if err != nil {
		respondError(c, 500, "could not process registration")
		return
	}

	userID := uuid.New()

	user, err := h.queries.CreateUser(c.Request.Context(), db.CreateUserParams{
		ID:           userID,
		Email:        email,
		PasswordHash: passwordHash,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			respondError(c, 409, "an account with this email already exists")
			return
		}

		respondError(c, 500, "could not create account")
		return
	}

	c.JSON(201, gin.H{
		"user": userResponse{
			ID:        user.ID,
			Email:     user.Email,
			CreatedAt: user.CreatedAt,
		},
	})
}

func (h *Handler) Login(c *gin.Context) {
	var req authRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, 400, "email and password are required")
		return
	}

	email, ok := normalizeEmail(req.Email)
	if !ok || len(req.Password) == 0 || len(req.Password) > 128 {
		respondError(c, 401, "invalid email or password")
		return
	}

	user, err := h.queries.GetUserByEmail(c.Request.Context(), email)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respondError(c, 401, "invalid email or password")
			return
		}

		respondError(c, 500, "could not process login")
		return
	}

	passwordMatches, err := CheckPasswordHash(req.Password, user.PasswordHash)
	if err != nil {
		respondError(c, 500, "could not process login")
		return
	}
	if !passwordMatches {
		respondError(c, 401, "invalid email or password")
		return
	}

	token, err := MakeJWT(user.ID, h.tokenSecret, h.tokenExpiry)
	if err != nil {
		respondError(c, 500, "could not create access token")
		return
	}

	c.JSON(200, loginResponse{
		User: userResponse{
			ID:        user.ID,
			Email:     user.Email,
			CreatedAt: user.CreatedAt,
		},
		AccessToken: token,
		TokenType:   "Bearer",
		ExpiresIn:   int64(h.tokenExpiry.Seconds()),
	})
}

func (h *Handler) Me(c *gin.Context) {
	value, exists := c.Get("userID")
	if !exists {
		respondError(c, 401, "authentication required")
		return
	}

	userID, ok := value.(uuid.UUID)
	if !ok {
		respondError(c, 401, "invalid authenticated user")
		return
	}

	user, err := h.queries.GetUserByID(c.Request.Context(), userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respondError(c, 401, "user account no longer exists")
			return
		}

		respondError(c, 500, "could not retrieve user")
		return
	}

	c.JSON(200, gin.H{
		"user": userResponse{
			ID:        user.ID,
			Email:     user.Email,
			CreatedAt: user.CreatedAt,
		},
	})
}
