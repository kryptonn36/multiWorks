package websocket

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"github.com/kryptonn36/multiWorks/internal/db"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const ticketTTL = 30 * time.Second

func ticketHash(ticket string) string {
	hash := sha256.Sum256([]byte(ticket))
	return hex.EncodeToString(hash[:])
}

// IssueTicket requires the normal bearer middleware; the credential is project-bound.
func (h *Handler) IssueTicket(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	value, exists := c.Get("userID")
	userID, valid := value.(uuid.UUID)
	if !exists || !valid || userID == uuid.Nil {
		c.JSON(401, gin.H{"error": "authentication required"})
		return
	}
	projectID, err := uuid.Parse(c.Param("projectID"))
	if err != nil || projectID == uuid.Nil {
		c.JSON(404, gin.H{"error": "project not found"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	if _, err = h.Store.Role(ctx, projectID, userID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(404, gin.H{"error": "project not found"})
		} else {
			c.JSON(500, gin.H{"error": "could not check project access"})
		}
		return
	}
	var secret [32]byte
	if _, err = rand.Read(secret[:]); err != nil {
		c.JSON(500, gin.H{"error": "could not create ticket"})
		return
	}
	ticket := base64.RawURLEncoding.EncodeToString(secret[:])
	if _, err = db.New(h.DB).DeleteExpiredSocketTickets(ctx); err != nil {
		c.JSON(500, gin.H{"error": "could not create ticket"})
		return
	}
	_, err = db.New(h.DB).CreateSocketTicket(ctx, db.CreateSocketTicketParams{TicketHash: ticketHash(ticket), ProjectID: projectID, UserID: userID})
	if err != nil {
		c.JSON(500, gin.H{"error": "could not create ticket"})
		return
	}
	c.JSON(201, gin.H{"ticket": ticket, "expires_in": int(ticketTTL.Seconds())})
}

func (h *Handler) consumeTicket(parent context.Context, projectID uuid.UUID, ticket string) (uuid.UUID, error) {
	var userID uuid.UUID
	if len(ticket) != 43 {
		return userID, errors.New("invalid ticket")
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	consumeSocketTicketResult, err := db.New(h.DB).ConsumeSocketTicket(ctx, db.ConsumeSocketTicketParams{TicketHash: ticketHash(ticket), ProjectID: projectID})
	userID = consumeSocketTicketResult
	return userID, err
}
