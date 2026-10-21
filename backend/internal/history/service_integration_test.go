package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/kryptonn36/multiWorks/internal/board"
	"github.com/kryptonn36/multiWorks/internal/ops"
)

func TestHistoryRestoreAndSnapshots(t *testing.T) {
	dsn := os.Getenv("OPS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set OPS_TEST_DATABASE_URL to a migrated PostgreSQL test database")
	}
	database, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	user, viewer, outsider, workspace, project := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := database.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		if _, err := database.Exec(`DELETE FROM workspaces WHERE id=$1`, workspace); err != nil {
			t.Error(err)
		}
		if _, err := database.Exec(`DELETE FROM users WHERE id IN ($1,$2,$3)`, user, viewer, outsider); err != nil {
			t.Error(err)
		}
	})
	for _, id := range []uuid.UUID{user, viewer, outsider} {
		exec(`INSERT INTO users(id,email,password_hash) VALUES($1,$2,'fixture')`, id, id.String()+"@history-test.invalid")
	}
	exec(`INSERT INTO workspaces(id,name,created_by) VALUES($1,'History test',$2)`, workspace, user)
	exec(`INSERT INTO workspace_members(workspace_id,user_id,role) VALUES($1,$2,'owner'),($1,$3,'member')`, workspace, user, viewer)
	createProject := func(id uuid.UUID) {
		exec(`INSERT INTO projects(id,workspace_id,name,created_by) VALUES($1,$2,'Historical board',$3)`, id, workspace, user)
		exec(`INSERT INTO project_members(project_id,user_id,role) VALUES($1,$2,'owner'),($1,$3,'viewer')`, id, user, viewer)
	}
	createProject(project)
	operations := ops.NewService(database)
	service := NewService(NewRepository(database), operations)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := uuid.NewString()
	scene, shot := uuid.NewString(), uuid.NewString()
	submit := func(kind string, payload any) ops.SubmitResponse {
		t.Helper()
		raw, _ := json.Marshal(payload)
		result, err := operations.Submit(ctx, project, user, ops.SubmitRequest{ClientID: client, OpID: uuid.NewString(), Type: kind, Payload: raw})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	at := func(pid, actor uuid.UUID, seq int64) board.State {
		t.Helper()
		state, err := service.GetAtSequence(ctx, pid, actor, seq)
		if err != nil {
			t.Fatal(err)
		}
		return state
	}
	expectStatus := func(err error, want int) {
		t.Helper()
		if err == nil || status(err) != want {
			t.Fatalf("got error %v status %d, want %d", err, status(err), want)
		}
	}
	submit("scene.create", map[string]any{"scene_id": scene, "title": "Opening"})
	submit("shot.create", map[string]any{"scene_id": scene, "shot_id": shot, "prompt": "Wide shot", "image_url": nil})
	submit("scene.update", map[string]any{"scene_id": scene, "title": "Later title"})
	if state := at(project, viewer, 2); state.Scenes[0].Title != "Opening" || len(state.Scenes[0].Shots) != 1 {
		t.Fatal("historical board mismatch")
	}
	if state := at(project, user, 0); len(state.Scenes) != 0 {
		t.Fatal("initial board was not empty")
	}
	_, err = service.GetAtSequence(ctx, project, user, 4)
	expectStatus(err, 400)
	_, err = service.GetAtSequence(ctx, project, outsider, 2)
	expectStatus(err, 404)
	listing, err := service.List(ctx, project, user, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if listing.CurrentSeq != 3 || len(listing.Snapshots) != 1 || listing.Snapshots[0].Seq != 0 || len(listing.Operations) != 3 {
		t.Fatal("history metadata mismatch")
	}
	request := ops.SubmitRequest{ClientID: client, OpID: "restore-original", BaseSeq: 3}
	_, err = service.Restore(ctx, project, viewer, 1, request)
	expectStatus(err, 403)
	restored, err := service.Restore(ctx, project, user, 1, request)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Operation.Seq != 4 || restored.Operation.Type != "restore_board" {
		t.Fatal("restore did not allocate a new operation")
	}
	if state := at(project, user, 4); state.Scenes[0].Title != "Opening" || len(state.Scenes[0].Shots) != 0 {
		t.Fatal("restored board mismatch")
	}
	if state := at(project, user, 3); state.Scenes[0].Title != "Later title" || len(state.Scenes[0].Shots) != 1 {
		t.Fatal("restore rewrote old history")
	}
	duplicate, err := service.Restore(ctx, project, user, 1, request)
	if err != nil || !duplicate.Duplicate || duplicate.Operation.Seq != 4 {
		t.Fatalf("restore retry was not deduplicated: %v", err)
	}
	_, err = service.Restore(ctx, project, user, 2, request)
	expectStatus(err, 409)
	_, err = service.Restore(ctx, project, user, 2, ops.SubmitRequest{ClientID: client, OpID: "stale", BaseSeq: 3})
	expectStatus(err, 409)
	// Caller-supplied replacement data is rejected; only server reconstruction is used.
	_, err = operations.Submit(ctx, project, user, ops.SubmitRequest{ClientID: client, OpID: "forged", BaseSeq: 4, Type: "restore_board", Payload: json.RawMessage(`{"seq":1,"board_state":{"scenes":[]}}`)})
	expectStatus(err, 400)
	submit("scene.update", map[string]any{"scene_id": scene, "description": "Concurrent edit"}) // 5
	_, err = service.Restore(ctx, project, user, 1, ops.SubmitRequest{ClientID: client, OpID: "prepared-before-edit", BaseSeq: 4})
	expectStatus(err, 409)
	raw, _ := json.Marshal(map[string]any{"seq": 1})
	if result, err := operations.Submit(ctx, project, user, ops.SubmitRequest{ClientID: client, OpID: "operation-transport-restore", BaseSeq: 5, Type: "restore_board", Payload: raw}); err != nil || result.Operation.Seq != 6 {
		t.Fatalf("shared engine restore failed: %v", err)
	}
	for seq := 7; seq <= 101; seq++ {
		result := submit("scene.update", map[string]any{"scene_id": scene, "title": fmt.Sprintf("Frame %d", seq)})
		if result.Operation.Seq != int64(seq) {
			t.Fatal("unexpected sequence")
		}
	}
	if state := at(project, user, 100); state.LastSeq != 100 || state.Scenes[0].Title != "Frame 100" {
		t.Fatal("periodic snapshot sequence does not match its board")
	}
	if state := at(project, user, 99); state.Scenes[0].Title != "Frame 99" {
		t.Fatal("snapshot plus replay mismatch")
	}
	if state := at(project, user, 101); state.Scenes[0].Title != "Frame 101" {
		t.Fatal("post-snapshot replay mismatch")
	}
	if result, err := service.Restore(ctx, project, user, 100, ops.SubmitRequest{ClientID: client, OpID: "restore-snapshot", BaseSeq: 101}); err != nil || result.Operation.Seq != 102 {
		t.Fatalf("snapshot restore failed: %v", err)
	}
	var snapshots, logs int
	if err := database.QueryRow(`SELECT COUNT(*) FROM project_snapshots WHERE project_id=$1 AND seq IN (0,4,6,100,102)`, project).Scan(&snapshots); err != nil || snapshots != 5 {
		t.Fatalf("missing precise snapshots: count=%d error=%v", snapshots, err)
	}
	// Two requests prepared against the same sequence cannot overwrite each other.
	type outcome struct {
		result ops.SubmitResponse
		err    error
	}
	outcomes := make(chan outcome, 2)
	for _, target := range []int64{1, 2} {
		go func(target int64) {
			result, err := service.Restore(ctx, project, user, target, ops.SubmitRequest{ClientID: client, OpID: fmt.Sprintf("race-%d", target), BaseSeq: 102})
			outcomes <- outcome{result, err}
		}(target)
	}
	successes, conflicts := 0, 0
	for range 2 {
		result := <-outcomes
		if result.err == nil && result.result.Operation.Seq == 103 {
			successes++
		} else if result.err != nil && status(result.err) == 409 {
			conflicts++
		} else {
			t.Fatalf("unexpected concurrent result: %+v", result)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatal("concurrent restore was not serialized")
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM project_ops WHERE project_id=$1`, project).Scan(&logs); err != nil || logs != 103 {
		t.Fatalf("history was deleted or rejected writes were logged: %d %v", logs, err)
	}
	// Legacy scenes are preserved in the initial checkpoint before the first edit.
	legacy := uuid.New()
	createProject(legacy)
	legacyScene := uuid.New()
	exec(`INSERT INTO scenes(id,project_id,title,created_by) VALUES($1,$2,'Legacy scene',$3)`, legacyScene, legacy, user)
	change, _ := json.Marshal(map[string]any{"scene_id": legacyScene, "title": "Legacy changed"})
	if _, err := operations.Submit(ctx, legacy, user, ops.SubmitRequest{ClientID: client, OpID: "legacy-update", Type: "scene.update", Payload: change}); err != nil {
		t.Fatal(err)
	}
	if state := at(legacy, user, 0); len(state.Scenes) != 1 || state.Scenes[0].Title != "Legacy scene" {
		t.Fatal("legacy baseline was lost")
	}
	if err := service.CreateSnapshot(ctx, legacy, user); err != nil {
		t.Fatal(err)
	}
	exec(`DELETE FROM project_snapshots WHERE project_id=$1 AND seq=0`, legacy)
	_, err = service.GetAtSequence(ctx, legacy, user, 0)
	expectStatus(err, 409) // Never fabricate unknown legacy state.
	// A complete pre-snapshot operation log can still reconstruct from the beginning.
	fresh := uuid.New()
	createProject(fresh)
	freshScene := uuid.New()
	payload, _ := json.Marshal(map[string]any{"scene_id": freshScene, "title": "Complete log"})
	if _, err := operations.Submit(ctx, fresh, user, ops.SubmitRequest{ClientID: client, OpID: "fresh", Type: "scene.create", Payload: payload}); err != nil {
		t.Fatal(err)
	}
	if err := service.CreateSnapshot(ctx, fresh, user); err != nil {
		t.Fatal(err)
	}
	exec(`DELETE FROM project_snapshots WHERE project_id=$1 AND seq=0`, fresh)
	if state := at(fresh, user, 0); len(state.Scenes) != 0 {
		t.Fatal("empty initial state was not reconstructed")
	}
}
