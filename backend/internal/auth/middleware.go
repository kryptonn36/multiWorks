package auth

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func AuthMiddleware(tokenSecret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenString, err := GetBearerToken(c.Request.Header)
		if err != nil {
			c.Header("WWW-Authenticate", "Bearer")
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "authentication required",
			})
			return
		}

		userID, err := ValidateJWT(tokenString, tokenSecret)
		if err != nil {
			c.Header("WWW-Authenticate", "Bearer")
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "invalid or expired access token",
			})
			return
		}

		c.Set("userID", userID)
		c.Next()
	}
}
