package auth

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func TestMiddlewareRejectsInvalidClaimsAndDoesNotRunHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	secret := "stage9-auth-unit-test-secret-32-characters"
	user := uuid.New()
	valid, err := MakeJWT(user, secret, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	signed := func(method jwt.SigningMethod, claims jwt.RegisteredClaims) string {
		t.Helper()
		token, err := jwt.NewWithClaims(method, claims).SignedString([]byte(secret))
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	claims := jwt.RegisteredClaims{Issuer: accessTokenIssuer, Subject: user.String(), ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute))}
	expired := claims
	expired.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Minute))
	wrongIssuer := claims
	wrongIssuer.Issuer = "other-app"
	missingExpiry := claims
	missingExpiry.ExpiresAt = nil
	invalidUser := claims
	invalidUser.Subject = uuid.Nil.String()
	for name, header := range map[string]string{"missing": "", "malformed": "Bearer one two", "tampered": "Bearer x" + valid, "expired": "Bearer " + signed(jwt.SigningMethodHS256, expired), "issuer": "Bearer " + signed(jwt.SigningMethodHS256, wrongIssuer), "no-expiry": "Bearer " + signed(jwt.SigningMethodHS256, missingExpiry), "algorithm": "Bearer " + signed(jwt.SigningMethodHS512, claims), "empty-user": "Bearer " + signed(jwt.SigningMethodHS256, invalidUser), "valid": "Bearer " + valid} {
		t.Run(name, func(t *testing.T) {
			called := false
			router := gin.New()
			router.Use(AuthMiddleware(secret))
			router.GET("/private", func(c *gin.Context) {
				called = true
				if c.MustGet("userID") != user {
					t.Error("wrong authenticated identity")
				}
				c.Status(204)
			})
			request := httptest.NewRequest("GET", "/private", nil)
			request.Header.Set("Authorization", header)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if name == "valid" {
				if response.Code != 204 || !called {
					t.Fatal("valid session rejected")
				}
			} else if response.Code != 401 || called || response.Header().Get("WWW-Authenticate") != "Bearer" {
				t.Fatalf("invalid session accepted: status=%d called=%v", response.Code, called)
			}
		})
	}
}
