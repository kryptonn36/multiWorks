package websocket

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	gorilla "github.com/gorilla/websocket"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/kryptonn36/multiWorks/internal/ops"
	"github.com/kryptonn36/multiWorks/internal/project"
	"github.com/kryptonn36/multiWorks/internal/workspace"
)

// Use an explicitly selected, migrated test database. Fixtures use unique IDs
// and are removed afterward; no existing workspace or account is modified.
func TestOperationTransports(t *testing.T) {
	dsn := os.Getenv("OPS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set OPS_TEST_DATABASE_URL to a migrated PostgreSQL test database")
	}
	database, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	owner, editor, viewer := uuid.New(), uuid.New(), uuid.New()
	workspaceID, projectID := uuid.New(), uuid.New()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := database.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, err := database.Exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		if err != nil {
			t.Error(err)
		}
		_, err = database.Exec(`DELETE FROM users WHERE id IN ($1,$2,$3)`, owner, editor, viewer)
		if err != nil {
			t.Error(err)
		}
	})
	for _, user := range []uuid.UUID{owner, editor, viewer} {
		exec(`INSERT INTO users(id,email,password_hash) VALUES($1,$2,'test fixture')`, user, user.String()+"@operations-test.invalid")
	}
	exec(`INSERT INTO workspaces(id,name,created_by) VALUES($1,'Transport verification',$2)`, workspaceID, owner)
	exec(`INSERT INTO projects(id,workspace_id,name,created_by) VALUES($1,$2,'Shared pipeline',$3)`, projectID, workspaceID, owner)
	for _, member := range []struct {
		user uuid.UUID
		role string
	}{{owner, "owner"}, {editor, "editor"}, {viewer, "viewer"}} {
		exec(`INSERT INTO workspace_members(workspace_id,user_id,role) VALUES($1,$2,'member')`, workspaceID, member.user)
		exec(`INSERT INTO project_members(project_id,user_id,role) VALUES($1,$2,$3)`, projectID, member.user, member.role)
	}
	hub := NewHub()
	service := ops.NewService(database)
	var published atomic.Int64
	service.SetPublisher(func(project uuid.UUID, op ops.Operation) {
		published.Add(1)
		// A separate connection must see the committed log row before publication.
		var exists bool
		if err := database.QueryRow(`SELECT EXISTS(SELECT 1 FROM project_ops WHERE id=$1)`, op.ID).Scan(&exists); err != nil || !exists {
			t.Errorf("publication preceded commit: %v", err)
		}
		raw, _ := json.Marshal(op)
		hub.Broadcast(project, ServerMessage{Type: "operation", Seq: op.Seq, Operation: raw})
	})
	socketHandler := NewHandler(database, hub, service)
	rest := ops.NewHandlerWithService(service)
	projectHandler := project.NewHandler(database)
	projectHandler.SetMembershipPublisher(hub.PermissionsChanged)
	workspaceHandler := workspace.NewHandler(database)
	workspaceHandler.SetMembershipPublisher(socketHandler.WorkspacePermissionsChanged)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	// Only this test server supplies a fixed authenticated identity. Production
	// keeps its JWT middleware and ticket authentication unchanged.
	authenticated := router.Group("/api", func(c *gin.Context) {
		id, err := uuid.Parse(c.GetHeader("X-Test-User"))
		if err != nil {
			c.AbortWithStatus(401)
			return
		}
		c.Set("userID", id)
	})
	authenticated.POST("/projects/:projectID/ops", rest.Submit)
	authenticated.POST("/projects/:projectID/members", projectHandler.AddMember)
	authenticated.POST("/workspaces/:workspaceID/members", workspaceHandler.AddMember)
	authenticated.POST("/projects/:projectID/ws-ticket", socketHandler.IssueTicket)
	router.GET("/api/projects/:projectID/ws", socketHandler.Connect)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	base := server.URL + "/api/projects/" + projectID.String()
	post := func(user uuid.UUID, path string, body any, want int) []byte {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req, _ := http.NewRequest("POST", base+path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Test-User", user.String())
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != want {
			t.Fatalf("POST %s: status %d, want %d: %s", path, response.StatusCode, want, data)
		}
		return data
	}
	connect := func(user uuid.UUID) *gorilla.Conn {
		t.Helper()
		var ticket struct {
			Ticket string `json:"ticket"`
		}
		if err := json.Unmarshal(post(user, "/ws-ticket", map[string]any{}, 201), &ticket); err != nil {
			t.Fatal(err)
		}
		conn, _, err := gorilla.DefaultDialer.Dial(strings.Replace(base, "http:", "ws:", 1)+"/ws?ticket="+ticket.Ticket, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		for {
			var message ServerMessage
			if err := conn.ReadJSON(&message); err != nil {
				t.Fatal(err)
			}
			if message.Type == "replay_complete" {
				break
			}
		}
		return conn
	}
	editorSocket, viewerSocket := connect(editor), connect(viewer)
	read := func(conn *gorilla.Conn, kind string) ServerMessage {
		t.Helper()
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		for {
			var message ServerMessage
			if err := conn.ReadJSON(&message); err != nil {
				t.Fatal(err)
			}
			if message.Type == kind {
				return message
			}
		}
	}
	submit := func(conn *gorilla.Conn, request ops.SubmitRequest, kind string) ServerMessage {
		t.Helper()
		if err := conn.WriteJSON(map[string]any{"type": "submit_op", "operation": request}); err != nil {
			t.Fatal(err)
		}
		return read(conn, kind)
	}
	sceneID := uuid.NewString()
	create := ops.SubmitRequest{ClientID: "shared-client", OpID: "create", Type: "scene.create", Payload: json.RawMessage(`{"scene_id":"` + sceneID + `","title":"Opening"}`)}
	// REST -> WebSocket retry shares the existing sequence and emits no new event.
	post(editor, "/ops", create, 201)
	duplicate := submit(editorSocket, create, "ack")
	if duplicate.Seq != 1 || duplicate.Duplicate == nil || !*duplicate.Duplicate {
		t.Fatalf("bad duplicate ack: %+v", duplicate)
	}
	if published.Load() != 1 {
		t.Fatal("duplicate was published")
	}
	// WebSocket -> REST retry has the same result and keeps the original actor.
	update := ops.SubmitRequest{ClientID: "shared-client", OpID: "update", BaseSeq: 1, Type: "scene.update", Payload: json.RawMessage(`{"scene_id":"` + sceneID + `","title":"Revised"}`)}
	ack := submit(editorSocket, update, "ack")
	if ack.Seq != 2 || ack.Duplicate == nil || *ack.Duplicate {
		t.Fatalf("bad write ack: %+v", ack)
	}
	var retried ops.SubmitResponse
	if err := json.Unmarshal(post(editor, "/ops", update, 200), &retried); err != nil {
		t.Fatal(err)
	}
	if !retried.Duplicate || retried.Operation.Seq != 2 || retried.Operation.ActorID != editor {
		t.Fatal("REST retry diverged from socket write")
	}
	// Viewer is connected and receives live operations, but cannot submit writes.
	if event := read(viewerSocket, "operation"); event.Seq != 1 {
		t.Fatal("viewer did not receive live operation")
	}
	denied := update
	denied.OpID = "viewer-attempt"
	if failure := submit(viewerSocket, denied, "error"); failure.Status != 403 || failure.OpID != denied.OpID {
		t.Fatalf("bad viewer error: %+v", failure)
	}
	post(viewer, "/ops", denied, 403)
	invalid := create
	invalid.OpID = "invalid"
	invalid.Payload = json.RawMessage(`{"scene_id":"` + uuid.NewString() + `","title":""}`)
	if failure := submit(editorSocket, invalid, "error"); failure.Status != 400 {
		t.Fatalf("bad validation error: %+v", failure)
	}
	post(editor, "/ops", invalid, 400)
	missing := update
	missing.ClientID = ""
	wsError := submit(editorSocket, missing, "error")
	var restError struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(post(editor, "/ops", missing, 400), &restError); err != nil {
		t.Fatal(err)
	}
	if wsError.Status != 400 || wsError.Message != restError.Error {
		t.Fatalf("transport validation diverged: socket=%+v rest=%s", wsError, restError.Error)
	}

	conflict := update
	conflict.Payload = json.RawMessage(`{"scene_id":"` + sceneID + `","title":"Different content"}`)
	if failure := submit(editorSocket, conflict, "error"); failure.Status != 409 {
		t.Fatalf("bad key conflict: %+v", failure)
	}
	post(editor, "/ops", conflict, 409)
	ahead := update
	ahead.OpID = "ahead"
	ahead.BaseSeq = 100
	if failure := submit(editorSocket, ahead, "error"); failure.Status != 409 {
		t.Fatalf("bad future sequence error: %+v", failure)
	}
	// An error never poisons the connection; a duplicate retry still succeeds.
	if ack := submit(editorSocket, update, "ack"); ack.Seq != 2 {
		t.Fatal("socket did not recover after errors")
	}
	// The real membership handler notifies the live writer after autocommit.
	started := time.Now()
	post(owner, "/members", map[string]any{"email": editor.String() + "@operations-test.invalid", "role": "viewer"}, 200)
	changed := read(editorSocket, "permissions")
	if changed.Role != "viewer" || time.Since(started) >= 2*time.Second {
		t.Fatalf("downgrade notification was not prompt: %+v", changed)
	}
	deniedEditor := update
	deniedEditor.OpID = "downgraded-editor"
	if failure := submit(editorSocket, deniedEditor, "error"); failure.Status != 403 {
		t.Fatalf("downgraded editor wrote: %+v", failure)
	}
	post(owner, "/members", map[string]any{"email": editor.String() + "@operations-test.invalid", "role": "editor"}, 200)
	if changed := read(editorSocket, "permissions"); changed.Role != "editor" {
		t.Fatal("upgrade did not reach live socket")
	}
	// Rejected membership updates must never publish a permission change.
	post(viewer, "/members", map[string]any{"email": editor.String() + "@operations-test.invalid", "role": "viewer"}, 403)
	var seq, count int64
	var title string
	if err := database.QueryRow(`SELECT last_seq,(SELECT COUNT(*) FROM project_ops WHERE project_id=$1),(SELECT title FROM scenes WHERE id=$2) FROM projects WHERE id=$1`, projectID, sceneID).Scan(&seq, &count, &title); err != nil {
		t.Fatal(err)
	}
	if seq != 2 || count != 2 || title != "Revised" || published.Load() != 2 {
		t.Fatalf("failed writes changed state: seq=%d count=%d title=%s published=%d", seq, count, title, published.Load())
	}
	// Simulate a committed membership removal and its post-commit hook.
	exec(`DELETE FROM project_members WHERE project_id=$1 AND user_id=$2`, projectID, editor)
	hub.PermissionsChanged(projectID, editor)
	if changed := read(editorSocket, "permissions"); !changed.AccessRevoked {
		t.Fatal("full access revocation was not sent")
	}
	if _, _, err := editorSocket.ReadMessage(); !gorilla.IsCloseError(err, gorilla.ClosePolicyViolation) {
		t.Fatalf("expected policy close, got %v", err)
	}
	// Out-of-band writes do not emit hooks; the five-second fallback still closes access.
	exec(`DELETE FROM project_members WHERE project_id=$1 AND user_id=$2`, projectID, viewer)
	viewerSocket.SetReadDeadline(time.Now().Add(8 * time.Second))
	for {
		var message ServerMessage
		if err := viewerSocket.ReadJSON(&message); err != nil {
			t.Fatal(err)
		}
		if message.Type == "permissions" {
			if !message.AccessRevoked {
				t.Fatal("fallback did not revoke missing membership")
			}
			break
		}
	}
	// An unauthenticated service call is rejected before reaching the database.
	if _, err := service.Submit(context.Background(), projectID, uuid.Nil, create); ops.ErrorStatus(err) != 401 {
		t.Fatal("missing actor was accepted")
	}
	editorSocket.Close()
	viewerSocket.Close()
}
