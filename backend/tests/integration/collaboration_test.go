package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	gorilla "github.com/gorilla/websocket"
	"github.com/kryptonn36/multiWorks/internal/board"
	"github.com/kryptonn36/multiWorks/internal/ops"
	realtime "github.com/kryptonn36/multiWorks/internal/websocket"
)

func TestIdempotencyConcurrencyAndReplayReconstruction(t *testing.T) {
	f := newFixture(t)
	scene := uuid.New()
	create := operation("scene.create", map[string]any{"scene_id": scene, "title": "Opening"})
	first := f.submit(t, f.editor, create)
	duplicate := decode[ops.SubmitResponse](t, f.json(t, f.editor, "POST", f.path("/ops"), create, 200))
	if !duplicate.Duplicate || duplicate.Operation.ID != first.Operation.ID || duplicate.Operation.Seq != 1 {
		t.Fatal("retry did not preserve the committed operation")
	}
	conflicting := create
	conflicting.Payload = json.RawMessage(`{"scene_id":"` + scene.String() + `","title":"Different content"}`)
	f.json(t, f.editor, "POST", f.path("/ops"), conflicting, 409)
	// Both clients use the same stale base. The actual policy is field-level LWW:
	// disjoint supplied fields merge; neither operation replaces omitted fields.
	edits := []ops.SubmitRequest{operation("scene.update", map[string]any{"scene_id": scene, "title": "Title from A"}), operation("scene.update", map[string]any{"scene_id": scene, "description": "Description from B"})}
	type outcome struct {
		response ops.SubmitResponse
		err      error
	}
	outcomes := make(chan outcome, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, edit := range edits {
		edit.BaseSeq = 1
		wg.Go(func() {
			<-start
			response, err := f.operations.Submit(context.Background(), f.project, f.editor.ID, edit)
			outcomes <- outcome{response, err}
		})
	}
	close(start)
	wg.Wait()
	close(outcomes)
	seen := map[int64]bool{}
	for result := range outcomes {
		if result.err != nil {
			t.Fatal(result.err)
		}
		seen[result.response.Operation.Seq] = true
	}
	if !seen[2] || !seen[3] {
		t.Fatal("concurrent operations did not allocate distinct sequences")
	}
	current := decode[board.State](t, f.json(t, f.owner, "GET", f.path("/board"), nil, 200))
	if current.Scenes[0].Title != "Title from A" || current.Scenes[0].Description != "Description from B" {
		t.Fatal("a concurrent edit lost an omitted field")
	}
	// Same-field edits retain both operations; the last sequence determines value.
	for _, title := range []string{"Earlier title", "Final title"} {
		f.submit(t, f.editor, operation("scene.update", map[string]any{"scene_id": scene, "title": title}))
	}
	before := decode[board.State](t, f.json(t, f.owner, "GET", f.path("/board"), nil, 200))
	f.json(t, f.editor, "POST", f.path("/ops"), operation("scene.reorder", map[string]any{"scene_ids": []uuid.UUID{uuid.New()}}), 409)
	f.json(t, f.editor, "POST", f.path("/ops"), operation("scene.update", map[string]any{"scene_id": uuid.New(), "title": "Missing"}), 409)
	after := decode[board.State](t, f.json(t, f.owner, "GET", f.path("/board"), nil, 200))
	if !reflect.DeepEqual(before, after) {
		t.Fatal("failed operation partially changed board or sequence")
	}
	reconstructed := board.State{ProjectID: f.project, Scenes: []board.Scene{}}
	var received []ops.Operation
	cursor := int64(0)
	for {
		page := decode[ops.ReplayResponse](t, f.json(t, f.owner, "GET", f.path(fmt.Sprintf("/ops?after_seq=%d&limit=2", cursor)), nil, 200))
		for _, op := range page.Operations {
			if op.Seq != cursor+1 {
				t.Fatalf("sequence gap/duplicate: %d after %d", op.Seq, cursor)
			}
			next, err := board.Apply(reconstructed, board.Operation{ProjectID: op.ProjectID, Seq: op.Seq, Type: op.Type, Payload: op.Payload, ActorID: op.ActorID, CreatedAt: op.CreatedAt})
			if err != nil {
				t.Fatal(err)
			}
			reconstructed = next
			cursor = op.Seq
			received = append(received, op)
		}
		if !page.HasMore {
			break
		}
		if len(page.Operations) == 0 {
			t.Fatal("replay pagination made no progress")
		}
	}
	if cursor != 5 || len(received) != 5 || f.published(f.project) != 5 {
		t.Fatalf("unexpected committed counts: cursor=%d ops=%d", cursor, len(received))
	}
	if !reflect.DeepEqual(board.Canonical(reconstructed), board.Canonical(after)) {
		t.Fatal("log replay did not reconstruct the stored board")
	}
	var count int
	if err := f.database.QueryRow(`SELECT COUNT(*) FROM project_ops WHERE project_id=$1 AND client_id=$2 AND op_id=$3`, f.project, create.ClientID, create.OpID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("idempotency row count %d: %v", count, err)
	}
	empty := decode[ops.ReplayResponse](t, f.json(t, f.owner, "GET", f.path("/ops?after_seq=5"), nil, 200))
	if empty.Operations == nil || len(empty.Operations) != 0 || empty.HasMore {
		t.Fatal("empty replay response must contain an empty array")
	}
}
func (f *fixture) ticket(t *testing.T, person actor) string {
	t.Helper()
	return decode[struct {
		Ticket string `json:"ticket"`
	}](t, f.json(t, person, "POST", f.path("/ws-ticket"), map[string]any{}, 201)).Ticket
}
func (f *fixture) connect(t *testing.T, person actor, after int64) (*gorilla.Conn, []ops.Operation) {
	t.Helper()
	url := strings.Replace(f.server.URL, "http:", "ws:", 1) + f.path(fmt.Sprintf("/ws?ticket=%s&last_seen_seq=%d", f.ticket(t, person), after))
	conn, _, err := gorilla.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	received := []ops.Operation{}
	for {
		var message realtime.ServerMessage
		if err := conn.ReadJSON(&message); err != nil {
			t.Fatal(err)
		}
		if message.Type == "replay" {
			received = append(received, decode[[]ops.Operation](t, message.Operations)...)
		}
		if message.Type == "replay_complete" {
			return conn, received
		}
	}
}
func readSocket(t *testing.T, conn *gorilla.Conn, kind string) realtime.ServerMessage {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		var message realtime.ServerMessage
		if err := conn.ReadJSON(&message); err != nil {
			t.Fatal(err)
		}
		if message.Type == kind {
			return message
		}
	}
}
func TestWebSocketReplayAfterHandlerRestartAndRevocation(t *testing.T) {
	f := newFixture(t)
	scene := uuid.New()
	f.submit(t, f.editor, operation("scene.create", map[string]any{"scene_id": scene, "title": "Before disconnect"}))
	client, replay := f.connect(t, f.editor, 0)
	if len(replay) != 1 || replay[0].Seq != 1 {
		t.Fatal("initial replay mismatch")
	}
	f.submit(t, f.owner, operation("scene.update", map[string]any{"scene_id": scene, "title": "Live edit"}))
	if message := readSocket(t, client, "operation"); message.Seq != 2 {
		t.Fatal("live sequence mismatch")
	}
	client.Close()
	f.submit(t, f.owner, operation("scene.update", map[string]any{"scene_id": scene, "description": "While disconnected"}))
	// Rebuild handlers/hub from the same DB, as on an API process restart.
	f.server.Close()
	f.start(t)
	f.submit(t, f.owner, operation("scene.update", map[string]any{"scene_id": scene, "title": "After restart"}))
	client, replay = f.connect(t, f.editor, 2)
	if len(replay) != 2 || replay[0].Seq != 3 || replay[1].Seq != 4 {
		t.Fatalf("missing or duplicate replay: %+v", replay)
	}
	f.json(t, f.owner, "POST", f.path("/members"), map[string]any{"email": f.editor.Email, "role": "viewer"}, 200)
	if message := readSocket(t, client, "permissions"); message.Role != "viewer" || !message.ReconnectRequired {
		t.Fatal("live downgrade was not signaled")
	}
	if _, _, err := client.ReadMessage(); !gorilla.IsCloseError(err, gorilla.CloseServiceRestart) {
		t.Fatalf("downgraded writer remained open: %v", err)
	}
	f.json(t, f.editor, "POST", f.path("/ops"), operation("scene.update", map[string]any{"scene_id": scene, "title": "Denied"}), 403)
	client, _ = f.connect(t, f.editor, 4)
	if err := client.WriteJSON(map[string]any{"type": "submit_op", "operation": operation("scene.update", map[string]any{"scene_id": scene, "title": "Denied"})}); err != nil {
		t.Fatal(err)
	}
	if message := readSocket(t, client, "error"); message.Status != 403 {
		t.Fatal("downgraded socket wrote successfully")
	}
	unused := f.ticket(t, f.editor)
	// There is no membership DELETE endpoint. Arrange a committed admin removal
	// and invoke the same post-commit hook; the audit documents this UI/API gap.
	f.exec(t, `DELETE FROM project_members WHERE project_id=$1 AND user_id=$2`, f.project, f.editor.ID)
	f.socket.MembershipChanged(f.project, f.editor.ID)
	if message := readSocket(t, client, "permissions"); !message.AccessRevoked {
		t.Fatal("access removal was not signaled")
	}
	f.json(t, f.editor, "POST", f.path("/ops"), operation("scene.update", map[string]any{"scene_id": scene, "title": "Denied after removal"}), 404)
	f.json(t, f.editor, "POST", f.path("/ws-ticket"), map[string]any{}, 404)
	f.json(t, f.editor, "GET", "/api/auth/me", nil, 200) // JWT remains valid; membership does not.
	conn, response, err := gorilla.DefaultDialer.Dial(strings.Replace(f.server.URL, "http:", "ws:", 1)+f.path("/ws?ticket="+unused), nil)
	if conn != nil {
		conn.Close()
	}
	if err == nil || response == nil || response.StatusCode != 404 {
		t.Fatal("a pre-issued ticket regained removed membership")
	}
	if response.Body != nil {
		response.Body.Close()
	}
	current := decode[board.State](t, f.json(t, f.owner, "GET", f.path("/board"), nil, 200))
	if current.LastSeq != 4 || current.Scenes[0].Title != "After restart" {
		t.Fatal("revoked operations changed the board")
	}
}
