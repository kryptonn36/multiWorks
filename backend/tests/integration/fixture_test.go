package integration

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/kryptonn36/multiWorks/internal/agent"
	"github.com/kryptonn36/multiWorks/internal/assets"
	"github.com/kryptonn36/multiWorks/internal/auth"
	"github.com/kryptonn36/multiWorks/internal/db"
	"github.com/kryptonn36/multiWorks/internal/history"
	"github.com/kryptonn36/multiWorks/internal/ops"
	"github.com/kryptonn36/multiWorks/internal/project"
	"github.com/kryptonn36/multiWorks/internal/storyboard"
	realtime "github.com/kryptonn36/multiWorks/internal/websocket"
	"github.com/kryptonn36/multiWorks/internal/workspace"
	"github.com/kryptonn36/multiWorks/tests/testsupport"
)

const testSecret = "stage9-integration-secret-at-least-32-characters"

type actor struct {
	ID           uuid.UUID
	Email, Token string
}
type fixture struct {
	database                *sql.DB
	server                  *httptest.Server
	operations              *ops.Service
	assets                  *assets.Service
	agent                   *agent.Service
	socket                  *realtime.Handler
	hub                     *realtime.Hub
	owner, editor, outsider actor
	workspace, project      uuid.UUID
	storage                 *assets.Storage
	mutex                   sync.Mutex
	events                  []ops.Operation
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{database: testsupport.IsolatedDatabase(t)}
	hash, err := auth.HashPassword("stage9-fixture-password")
	if err != nil {
		t.Fatal(err)
	}
	for _, person := range []*actor{&f.owner, &f.editor, &f.outsider} {
		person.ID = uuid.New()
		person.Email = person.ID.String() + "@stage9.invalid"
		_, err := db.New(f.database).CreateUser(context.Background(), db.CreateUserParams{ID: person.ID, Email: person.Email, PasswordHash: hash})
		if err != nil {
			t.Fatal(err)
		}
		person.Token, err = auth.MakeJWT(person.ID, testSecret, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
	}
	f.storage, err = assets.NewStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f.start(t)
	w := decode[struct{ ID uuid.UUID }](t, f.json(t, f.owner, "POST", "/api/workspaces", map[string]any{"name": "Stage 9 isolated workspace"}, 201))
	f.workspace = w.ID
	f.json(t, f.owner, "POST", "/api/workspaces/"+w.ID.String()+"/members", map[string]any{"email": f.editor.Email, "role": "member"}, 200)
	f.project = f.newProject(t, "Stage 9 board")
	f.json(t, f.owner, "POST", f.path("/members"), map[string]any{"email": f.editor.Email, "role": "editor"}, 200)
	return f
}
func (f *fixture) start(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	f.operations = ops.NewService(f.database)
	f.hub = realtime.NewHub()
	f.socket = realtime.NewHandler(f.database, f.hub, f.operations)
	f.operations.SetPublisher(func(pid uuid.UUID, op ops.Operation) {
		var exists bool
		if err := f.database.QueryRow(`SELECT EXISTS(SELECT 1 FROM project_ops WHERE id=$1)`, op.ID).Scan(&exists); err != nil || !exists {
			t.Errorf("operation published before commit: %v", err)
		}
		f.mutex.Lock()
		f.events = append(f.events, op)
		f.mutex.Unlock()
		raw, _ := json.Marshal(op)
		f.hub.Broadcast(pid, realtime.ServerMessage{Type: "operation", Seq: op.Seq, Operation: raw})
	})
	f.assets = assets.NewService(f.database, f.storage, f.operations, assets.Limits{MaxUploadBytes: 1024, MaxPixels: 25000000})
	f.agent = agent.NewService(f.database, f.operations, agent.Limits{MaxOps: 20, Timeout: 3 * time.Second})
	ah := auth.NewHandler(db.New(f.database), testSecret, time.Hour)
	wh := workspace.NewHandler(f.database)
	ph := project.NewHandler(f.database)
	oh := ops.NewHandlerWithService(f.operations)
	bh := storyboard.NewHandler(f.database)
	hh := history.NewHandler(history.NewService(history.NewRepository(f.database), f.operations))
	assetsHTTP := assets.NewHandler(f.assets)
	agentHTTP := agent.NewHandler(f.agent)
	ph.SetMembershipPublisher(f.socket.MembershipChanged)
	wh.SetMembershipPublisher(f.socket.WorkspacePermissionsChanged)
	router := gin.New()
	router.Use(gin.Recovery())
	router.POST("/api/auth/register", ah.Register)
	router.POST("/api/auth/login", ah.Login)
	api := router.Group("/api", auth.AuthMiddleware(testSecret))
	api.GET("/auth/me", ah.Me)
	api.POST("/workspaces", wh.Create)
	api.POST("/workspaces/:workspaceID/members", wh.AddMember)
	api.POST("/workspaces/:workspaceID/projects", ph.Create)
	api.GET("/projects/:projectID", ph.Get)
	api.POST("/projects/:projectID/members", ph.AddMember)
	api.POST("/projects/:projectID/ops", oh.Submit)
	api.GET("/projects/:projectID/ops", oh.Replay)
	api.GET("/projects/:projectID/board", bh.GetBoard)
	api.GET("/projects/:projectID/history/:seq", hh.GetAtSequence)
	api.POST("/projects/:projectID/ws-ticket", f.socket.IssueTicket)
	router.GET("/api/projects/:projectID/ws", f.socket.Connect)
	api.POST("/projects/:projectID/shots/:shotID/assets", assetsHTTP.Upload)
	api.GET("/projects/:projectID/assets/:assetID", assetsHTTP.Download)
	api.GET("/projects/:projectID/assets/:assetID/thumbnail", assetsHTTP.Thumbnail)
	api.POST("/projects/:projectID/agent/propose", agentHTTP.Generate)
	api.GET("/projects/:projectID/agent/proposals/:proposalID", agentHTTP.Get)
	api.POST("/projects/:projectID/agent/proposals/:proposalID/approve", agentHTTP.Approve)
	api.POST("/projects/:projectID/agent/proposals/:proposalID/reject", agentHTTP.Reject)
	f.server = httptest.NewServer(router)
	t.Cleanup(f.server.Close)
}
func (f *fixture) path(suffix string) string { return "/api/projects/" + f.project.String() + suffix }
func (f *fixture) newProject(t *testing.T, name string) uuid.UUID {
	t.Helper()
	return decode[struct{ ID uuid.UUID }](t, f.json(t, f.owner, "POST", "/api/workspaces/"+f.workspace.String()+"/projects", map[string]any{"name": name}, 201)).ID
}
func (f *fixture) request(t *testing.T, person actor, method, path string, body io.Reader, contentType string, want int) []byte {
	t.Helper()
	request, err := http.NewRequest(method, f.server.URL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	if person.Token != "" {
		request.Header.Set("Authorization", "Bearer "+person.Token)
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := f.server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != want {
		t.Fatalf("%s %s: status %d want %d: %s", method, path, response.StatusCode, want, raw)
	}
	return raw
}
func (f *fixture) json(t *testing.T, person actor, method, path string, body any, want int) []byte {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	return f.request(t, person, method, path, reader, "application/json", want)
}
func decode[T any](t *testing.T, raw []byte) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	return value
}
func operation(kind string, payload any) ops.SubmitRequest {
	raw, _ := json.Marshal(payload)
	return ops.SubmitRequest{ClientID: "stage9", OpID: uuid.NewString(), Type: kind, Payload: raw}
}
func (f *fixture) submit(t *testing.T, person actor, request ops.SubmitRequest) ops.SubmitResponse {
	t.Helper()
	return decode[ops.SubmitResponse](t, f.json(t, person, "POST", f.path("/ops"), request, 201))
}
func (f *fixture) published(project uuid.UUID) int {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	count := 0
	for _, event := range f.events {
		if event.ProjectID == project {
			count++
		}
	}
	return count
}
func (f *fixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := f.database.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}
