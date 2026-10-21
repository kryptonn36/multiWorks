package auth

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPasswordHash(t *testing.T) {
	password := "correct-horse-battery-staple"

	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}

	if hash == password {
		t.Fatal("password hash must not equal the original password")
	}

	ok, err := CheckPasswordHash(password, hash)
	if err != nil {
		t.Fatalf("CheckPasswordHash() error = %v", err)
	}
	if !ok {
		t.Fatal("expected the correct password to match")
	}

	ok, err = CheckPasswordHash("wrong-password", hash)
	if err != nil {
		t.Fatalf("CheckPasswordHash() error = %v", err)
	}
	if ok {
		t.Fatal("expected the incorrect password not to match")
	}
}

func TestJWTValidation(t *testing.T) {
	secret := "test-secret-for-jwt-that-is-long-enough"
	userID := uuid.New()

	token, err := MakeJWT(userID, secret, time.Minute)
	if err != nil {
		t.Fatalf("MakeJWT() error = %v", err)
	}

	gotID, err := ValidateJWT(token, secret)
	if err != nil {
		t.Fatalf("ValidateJWT() error = %v", err)
	}
	if gotID != userID {
		t.Fatalf("got user ID %s, want %s", gotID, userID)
	}

	if _, err := ValidateJWT(token, "different-secret-that-is-also-long-enough"); err == nil {
		t.Fatal("expected token validation to fail with a different secret")
	}

	tampered := "x" + token[1:]
	if _, err := ValidateJWT(tampered, secret); err == nil {
		t.Fatal("expected token validation to fail after tampering")
	}
}

func TestMakeJWTRejectsInvalidExpiry(t *testing.T) {
	_, err := MakeJWT(
		uuid.New(),
		"test-secret-for-jwt-that-is-long-enough",
		0,
	)
	if err == nil {
		t.Fatal("expected non-positive token expiry to be rejected")
	}
}

func TestGetBearerToken(t *testing.T) {
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer example-token")

	got, err := GetBearerToken(headers)
	if err != nil {
		t.Fatalf("GetBearerToken() error = %v", err)
	}
	if got != "example-token" {
		t.Fatalf("got %q, want %q", got, "example-token")
	}
}

func TestGetBearerTokenRejectsMalformedHeaders(t *testing.T) {
	tests := []string{
		"",
		"example-token",
		"Bearer",
		"Bearer one two",
	}

	for _, header := range tests {
		t.Run(strings.ReplaceAll(header, " ", "_"), func(t *testing.T) {
			headers := make(http.Header)
			headers.Set("Authorization", header)

			if _, err := GetBearerToken(headers); err == nil {
				t.Fatalf("expected malformed header %q to fail", header)
			}
		})
	}
}
