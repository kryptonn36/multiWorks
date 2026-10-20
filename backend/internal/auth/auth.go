package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/alexedwards/argon2id"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func HashPassword(password string) (string,error) {
	hashed_paswd, err := argon2id.CreateHash(password, argon2id.DefaultParams)
	if err!=nil{
		return "",err
	}
	return hashed_paswd, nil
}

func CheckPasswordHash(password, hash string) (bool, error){
	boolean, err := argon2id.ComparePasswordAndHash(password,hash)
	if err!= nil{
		return false,err
	}
	return boolean,nil
}

func MakeJWT(userID uuid.UUID, tokenSecret string, expiresIn time.Duration) (string, error){
	claims := jwt.RegisteredClaims{
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(expiresIn)),
		Issuer: "chirpy-access",
		IssuedAt: jwt.NewNumericDate(time.Now()),
		Subject: userID.String(),
	}
	newJwt := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := newJwt.SignedString([]byte(tokenSecret))
	if err!=nil{
		return "", err
	}
	return tokenString, err
}

func ValidateJWT(tokenString, tokenSecret string) (uuid.UUID, error){
	claims := &jwt.RegisteredClaims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token)(interface{}, error){
		if _,ok:= token.Method.(*jwt.SigningMethodHMAC); !ok{
			return nil, fmt.Errorf("unexpected signing method")
		}
		return []byte(tokenSecret), nil
	})
	if err != nil {
		return uuid.Nil, err
	}

	if !token.Valid {
		return uuid.Nil, fmt.Errorf("invalid token")
	}

	// Extract user ID from Subject
	userID, err := uuid.Parse(claims.Subject)
	if err != nil {
		return uuid.Nil, err
	}

	return userID, nil
}

func GetBearerToken(headers http.Header) (string, error) {
	authHeader := headers.Get("Authorization")
	if authHeader == "" {
		return "", fmt.Errorf("authorization header does not exist")
	}

	const prefix = "Bearer "

	if !strings.HasPrefix(authHeader, prefix) {
		return "", fmt.Errorf("malformed authorization header")
	}

	token := strings.TrimPrefix(authHeader, prefix)
	return token, nil
}

func MakeRefreshToken() (string, error){
	key := make([]byte, 32)
	rand.Read(key)
	hexStr := hex.EncodeToString(key)
	return hexStr,nil
}