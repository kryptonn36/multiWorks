package assets_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/kryptonn36/multiWorks/internal/agent"
	"github.com/kryptonn36/multiWorks/internal/assets"
	"github.com/kryptonn36/multiWorks/internal/board"
	"github.com/kryptonn36/multiWorks/internal/db"
	"github.com/kryptonn36/multiWorks/internal/history"
	"github.com/kryptonn36/multiWorks/internal/ops"
)

// A separate schema isolates job claiming from the running application's worker.
func fixture(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("OPS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set OPS_TEST_DATABASE_URL to PostgreSQL")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "stage8_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	target, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	query := target.Query()
	query.Set("search_path", schema)
	target.RawQuery = query.Encode()
	database, err := sql.Open("pgx", target.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		database.Close()
		_, err := admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
		if err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	files, err := filepath.Glob("../../sql/migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		up := strings.Split(strings.Split(string(raw), "-- +goose Up")[1], "-- +goose Down")[0]
		if _, err = database.Exec(up); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
	}
	return database
}
func imageBytes(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
func status(t *testing.T, err error, want int) {
	t.Helper()
	if err == nil || ops.ErrorStatus(err) != want {
		t.Fatalf("error=%v, want status %d", err, want)
	}
}
func TestStage8AssetsAgentAndHistory(t *testing.T) {
	database := fixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := database.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	user, viewer, outsider, w, p, other := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{user, viewer, outsider} {
		exec(`INSERT INTO users(id,email,password_hash) VALUES($1,$2,'fixture')`, id, id.String()+"@stage8.invalid")
	}
	exec(`INSERT INTO workspaces(id,name,created_by) VALUES($1,'Stage 8',$2)`, w, user)
	exec(`INSERT INTO workspace_members(workspace_id,user_id,role) VALUES($1,$2,'owner'),($1,$3,'member')`, w, user, viewer)
	for _, pid := range []uuid.UUID{p, other} {
		exec(`INSERT INTO projects(id,workspace_id,name,created_by) VALUES($1,$2,'Stage 8',$3)`, pid, w, user)
		exec(`INSERT INTO project_members(project_id,user_id,role) VALUES($1,$2,'owner'),($1,$3,'viewer')`, pid, user, viewer)
	}
	operations := ops.NewService(database)
	historical := history.NewService(history.NewRepository(database), operations)
	var published []ops.Operation
	operations.SetPublisher(func(_ uuid.UUID, op ops.Operation) { published = append(published, op) })
	submit := func(pid uuid.UUID, kind string, payload any) ops.Operation {
		t.Helper()
		raw, _ := json.Marshal(payload)
		res, err := operations.Submit(ctx, pid, user, ops.SubmitRequest{ClientID: "stage8-test", OpID: uuid.NewString(), Type: kind, Payload: raw})
		if err != nil {
			t.Fatal(err)
		}
		return res.Operation
	}
	scene, shot, shot2, otherScene, otherShot := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	submit(p, "scene.create", map[string]any{"scene_id": scene, "title": "Opening"})
	for _, id := range []uuid.UUID{shot, shot2} {
		submit(p, "shot.create", map[string]any{"scene_id": scene, "shot_id": id})
	}
	submit(other, "scene.create", map[string]any{"scene_id": otherScene, "title": "Other"})
	submit(other, "shot.create", map[string]any{"scene_id": otherScene, "shot_id": otherShot})
	storage, err := assets.NewStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := assets.NewService(database, storage, operations)
	raw := imageBytes(t, 640, 320)
	first, err := service.Upload(ctx, p, shot, user, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := service.Upload(ctx, p, shot2, user, bytes.NewReader(raw))
	if err != nil || !duplicate.Deduped || duplicate.Asset.ID != first.Asset.ID {
		t.Fatalf("dedup: %v %+v", err, duplicate)
	}
	var blobs, jobs, links int
	if err := database.QueryRow(`SELECT (SELECT COUNT(*) FROM assets),(SELECT COUNT(*) FROM asset_jobs),(SELECT COUNT(*) FROM shot_assets)`).Scan(&blobs, &jobs, &links); err != nil || blobs != 1 || jobs != 1 || links != 2 {
		t.Fatalf("dedup counts %d %d %d: %v", blobs, jobs, links, err)
	}
	originals := 0
	filepath.WalkDir(storage.Root, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			originals++
		}
		return err
	})
	if originals != 1 {
		t.Fatalf("stored %d files", originals)
	}
	_, err = service.Upload(ctx, p, shot, viewer, bytes.NewReader(raw))
	status(t, err, 403)
	_, err = service.Upload(ctx, p, otherShot, user, bytes.NewReader(raw))
	status(t, err, 404)
	_, err = service.Get(ctx, other, first.Asset.ID, user)
	status(t, err, 404)
	_, err = service.Get(ctx, p, first.Asset.ID, outsider)
	status(t, err, 404)
	_, err = service.Upload(ctx, p, shot, user, strings.NewReader("<svg/>"))
	status(t, err, 400)
	_, err = service.Upload(ctx, p, shot, user, bytes.NewReader(make([]byte, assets.MaxUploadBytes+1)))
	status(t, err, 413)
	_, err = service.Upload(ctx, p, shot, user, bytes.NewReader(imageBytes(t, 8193, 1)))
	status(t, err, 400)
	// A new service instance recovers a leased job, as after a process crash.
	claimed, err := db.New(database).ClaimAssetJob(ctx)
	if err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE asset_jobs SET locked_at=NOW()-INTERVAL '61 seconds' WHERE id=$1`, claimed.ID)
	restarted := assets.NewService(database, storage, operations)
	var event assets.Asset
	restarted.Publish = func(_ uuid.UUID, a assets.Asset) { event = a }
	if ok, err := restarted.ProcessOne(ctx); !ok || err != nil {
		t.Fatalf("worker %v %v", ok, err)
	}
	if event.Status != "ready" || event.ID != first.Asset.ID {
		t.Fatalf("no ready event: %+v", event)
	}
	row, err := service.Get(ctx, p, first.Asset.ID, viewer)
	if err != nil {
		t.Fatal(err)
	}
	file, err := storage.Open(row.ThumbnailKey.String)
	if err != nil {
		t.Fatal(err)
	}
	config, _, err := image.DecodeConfig(file)
	file.Close()
	if err != nil || config.Width != 320 || config.Height != 160 {
		t.Fatalf("thumbnail %+v %v", config, err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("userID", viewer) })
	handler := assets.NewHandler(service)
	router.GET("/api/projects/:projectID/assets/:assetID", handler.Download)
	router.GET("/api/projects/:projectID/assets/:assetID/thumbnail", handler.Thumbnail)
	for _, path := range []string{row.ID.String(), row.ID.String() + "/thumbnail"} {
		req := httptest.NewRequest("GET", "/api/projects/"+p.String()+"/assets/"+path, nil)
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		if res.Code != 200 || res.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("protected file %d %s", res.Code, res.Body.String())
		}
	}
	// Attachments survive log reconstruction and restore, including two references.
	state, err := historical.GetAtSequence(ctx, p, user, 5)
	if err != nil || len(state.Scenes[0].Shots[0].AssetIDs) != 1 || len(state.Scenes[0].Shots[1].AssetIDs) != 1 {
		t.Fatalf("asset history: %v %+v", err, state)
	}
	submit(p, "shot.delete", map[string]any{"scene_id": scene, "shot_id": shot})
	if _, err = historical.Restore(ctx, p, user, 5, ops.SubmitRequest{ClientID: "restore", OpID: "assets", BaseSeq: 6}); err != nil {
		t.Fatal(err)
	}
	state, err = historical.GetAtSequence(ctx, p, user, 7)
	if err != nil || len(state.Scenes[0].Shots) != 2 || len(state.Scenes[0].Shots[0].AssetIDs) != 1 {
		t.Fatalf("asset restore %v", err)
	}
	// A missing original retries durably, then fails both asset and job safely.
	missing, err := service.Upload(ctx, p, shot, user, bytes.NewReader(imageBytes(t, 3, 3)))
	if err != nil {
		t.Fatal(err)
	}
	missingRow, _ := service.Get(ctx, p, missing.Asset.ID, user)
	if err = os.Remove(filepath.Join(storage.Root, missingRow.StorageKey)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err = restarted.ProcessOne(ctx); err != nil {
			t.Fatal(err)
		}
		exec(`UPDATE asset_jobs SET available_at=NOW() WHERE asset_id=$1`, missing.Asset.ID)
	}
	failed, _ := service.Get(ctx, p, missing.Asset.ID, user)
	if failed.Status != "failed" || !failed.ErrorMessage.Valid {
		t.Fatal("failed job not surfaced")
	}
	// Proposals are read-only until approved; failed mocks never mutate the log.
	agents := agent.NewService(database, operations)
	for _, mode := range []string{"malformed", "invalid", "oversized", "timeout"} {
		proposal, err := agents.Generate(ctx, p, user, "Add a dramatic shot", mode)
		if err != nil || proposal.Status != "failed" || len(proposal.Operations) != 0 {
			t.Fatalf("mode %s: %v %+v", mode, err, proposal)
		}
	}
	_, err = agents.Generate(ctx, p, viewer, "viewer edit", "normal")
	status(t, err, 403)
	before := len(published)
	proposal, err := agents.Generate(ctx, p, user, "Add a dramatic shot", "normal")
	if err != nil || proposal.Status != "pending" || len(published) != before {
		t.Fatalf("generation changed board: %v", err)
	}
	_, err = agents.Get(ctx, other, user, proposal.ID)
	status(t, err, 404)
	_, err = agents.Decide(ctx, p, viewer, proposal.ID, true)
	status(t, err, 403)
	approved, err := agents.Decide(ctx, p, user, proposal.ID, true)
	if err != nil || approved.Status != "approved" || len(approved.AppliedOperations) != 1 || len(published) != before+1 {
		t.Fatalf("approval %v %+v", err, approved)
	}
	if _, err = agents.Decide(ctx, p, user, proposal.ID, true); err != nil || len(published) != before+1 {
		t.Fatalf("approval retry duplicated: %v", err)
	}
	stale, err := agents.Generate(ctx, p, user, "Stale draft", "normal")
	if err != nil {
		t.Fatal(err)
	}
	submit(p, "scene.update", map[string]any{"scene_id": scene, "description": "Changed after generation"})
	_, err = agents.Decide(ctx, p, user, stale.ID, true)
	status(t, err, 409)
	stale, err = agents.Get(ctx, p, user, stale.ID)
	if err != nil || stale.Status != "stale" {
		t.Fatal("stale decision was not committed")
	}
	rejected, err := agents.Generate(ctx, p, user, "Reject draft", "delayed")
	if err != nil {
		t.Fatal(err)
	}
	rejected, err = agents.Decide(ctx, p, user, rejected.ID, false)
	if err != nil || rejected.Status != "rejected" {
		t.Fatalf("reject %v", err)
	}
	// A batch that fails its second SQL mutation must leave its first mutation,
	// sequence allocation, snapshot and log append rolled back, with no broadcast.
	before = len(published)
	_, err = operations.Transact(ctx, p, user, func(_ *sql.Tx, apply func(ops.SubmitRequest) (ops.SubmitResponse, error)) error {
		body, _ := json.Marshal(map[string]any{"scene_id": scene, "title": "Must roll back"})
		if _, err := apply(ops.SubmitRequest{ClientID: "atomic", OpID: "one", Type: "scene.update", Payload: body}); err != nil {
			return err
		}
		return errors.New("forced batch failure")
	})
	if err == nil || len(published) != before {
		t.Fatal("failed batch was published")
	}
	tx, _ := database.BeginTx(ctx, nil)
	current, err := board.Load(ctx, tx, p)
	tx.Rollback()
	if err != nil || current.Scenes[0].Title == "Must roll back" {
		t.Fatalf("partial batch persisted: %v", err)
	}
	// SQL rejection of a later approved edit rolls the entire proposal back.
	rollback, err := agents.Generate(ctx, p, user, "Draft for atomic rollback", "normal")
	if err != nil {
		t.Fatal(err)
	}
	newScene := uuid.New()
	one, _ := json.Marshal(map[string]any{"scene_id": newScene, "title": "Must not persist"})
	two, _ := json.Marshal(map[string]any{"scene_id": newScene, "shot_id": otherShot, "prompt": "Conflicts with another project's UUID"})
	batch, _ := json.Marshal([]ops.ProposedOperation{{Type: "scene.create", Payload: one}, {Type: "shot.create", Payload: two}})
	exec(`UPDATE agent_proposals SET proposed_ops=$1 WHERE id=$2`, batch, rollback.ID)
	before = len(published)
	_, err = agents.Decide(ctx, p, user, rollback.ID, true)
	status(t, err, 500)
	if len(published) != before {
		t.Fatal("failed approval published operations")
	}
	rollback, err = agents.Get(ctx, p, user, rollback.ID)
	if err != nil || rollback.Status != "pending" {
		t.Fatal("failed approval changed proposal status")
	}
	var partial int
	database.QueryRow(`SELECT COUNT(*) FROM scenes WHERE id=$1`, newScene).Scan(&partial)
	if partial != 0 {
		t.Fatal("failed approval committed its first edit")
	}
	// Simultaneous approvals serialize on the project and return one applied batch.
	concurrent, err := agents.Generate(ctx, p, user, "Concurrent draft", "normal")
	if err != nil {
		t.Fatal(err)
	}
	operations.SetPublisher(nil) // No unsynchronized test collector during concurrent callbacks.
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Go(func() { _, err := agents.Decide(ctx, p, user, concurrent.ID, true); errs <- err })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var count int
	database.QueryRow(`SELECT COUNT(*) FROM project_ops WHERE project_id=$1 AND client_id=$2`, p, "agent:"+concurrent.ID.String()).Scan(&count)
	if count != 1 {
		t.Fatalf("concurrent approval produced %d ops", count)
	}
}
