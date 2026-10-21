package integration

import (
	"bytes"
	"context"
	"github.com/google/uuid"
	"github.com/kryptonn36/multiWorks/internal/auth"
	"github.com/kryptonn36/multiWorks/internal/db"
	"testing"
)

func TestRegistrationLoginJWTAndProjectRoles(t *testing.T) {
	f := newFixture(t)
	email := uuid.NewString() + "@auth-stage9.invalid"
	password := "stage9-real-registration-password"
	raw := f.json(t, actor{}, "POST", "/api/auth/register", map[string]any{"email": email, "password": password}, 201)
	if bytes.Contains(raw, []byte("password_hash")) || bytes.Contains(raw, []byte(password)) {
		t.Fatal("registration leaked password data")
	}
	row, err := db.New(f.database).GetUserByEmail(context.Background(), email)
	if err != nil {
		t.Fatal(err)
	}
	matches, err := auth.CheckPasswordHash(password, row.PasswordHash)
	if err != nil || !matches || row.PasswordHash == password {
		t.Fatal("password was not stored securely")
	}
	f.json(t, actor{}, "POST", "/api/auth/register", map[string]any{"email": email, "password": password}, 409)
	f.json(t, actor{}, "POST", "/api/auth/login", map[string]any{"email": email, "password": "wrong"}, 401)
	login := decode[struct {
		AccessToken string `json:"access_token"`
	}](t, f.json(t, actor{}, "POST", "/api/auth/login", map[string]any{"email": email, "password": password}, 200))
	f.json(t, actor{Token: login.AccessToken}, "GET", "/api/auth/me", nil, 200)
	f.json(t, actor{}, "GET", f.path("/board"), nil, 401)
	f.json(t, actor{Token: "tampered"}, "GET", f.path("/board"), nil, 401)
	f.json(t, f.outsider, "GET", f.path(""), nil, 404)
	f.json(t, f.editor, "POST", f.path("/members"), map[string]any{"email": f.outsider.Email, "role": "editor"}, 403)
	f.json(t, f.owner, "POST", f.path("/members"), map[string]any{"email": f.outsider.Email, "role": "editor"}, 404)
	f.json(t, f.owner, "POST", f.path("/members"), map[string]any{"email": f.editor.Email, "role": "administrator"}, 400)
}
