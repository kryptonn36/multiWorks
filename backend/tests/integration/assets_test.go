package integration

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/kryptonn36/multiWorks/internal/assets"
	"github.com/kryptonn36/multiWorks/internal/board"
	"github.com/kryptonn36/multiWorks/internal/db"
)

func imageData(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	img.Set(0, 0, color.RGBA{R: 200, A: 255})
	var raw bytes.Buffer
	if err := png.Encode(&raw, img); err != nil {
		t.Fatal(err)
	}
	return raw.Bytes()
}
func (f *fixture) upload(t *testing.T, person actor, project, shot uuid.UUID, raw []byte, want int) []byte {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	file, err := writer.CreateFormFile("file", "test-image.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	return f.request(t, person, "POST", "/api/projects/"+project.String()+"/shots/"+shot.String()+"/assets", &body, writer.FormDataContentType(), want)
}
func TestAssetHTTPDedupIsolationAndSharedBlobLifetime(t *testing.T) {
	f := newFixture(t)
	scene, firstShot, secondShot := uuid.New(), uuid.New(), uuid.New()
	f.submit(t, f.owner, operation("scene.create", map[string]any{"scene_id": scene, "title": "Shared images"}))
	for _, shot := range []uuid.UUID{firstShot, secondShot} {
		f.submit(t, f.owner, operation("shot.create", map[string]any{"scene_id": scene, "shot_id": shot}))
	}
	raw := imageData(t)
	first := decode[assets.UploadResult](t, f.upload(t, f.editor, f.project, firstShot, raw, 201))
	duplicate := decode[assets.UploadResult](t, f.upload(t, f.editor, f.project, secondShot, raw, 201))
	if first.Deduped || !duplicate.Deduped || first.Asset.ID != duplicate.Asset.ID || first.Asset.Status != "processing" {
		t.Fatal("HTTP upload/dedup contract mismatch")
	}
	var records, jobs, references int
	if err := f.database.QueryRow(`SELECT (SELECT COUNT(*) FROM assets),(SELECT COUNT(*) FROM asset_jobs),(SELECT COUNT(*) FROM shot_assets)`).Scan(&records, &jobs, &references); err != nil || records != 1 || jobs != 1 || references != 2 {
		t.Fatalf("dedup database mismatch %d %d %d: %v", records, jobs, references, err)
	}
	files := 0
	if err := filepath.WalkDir(f.storage.Root, func(_ string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			files++
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if files != 1 {
		t.Fatalf("duplicate upload stored %d blobs", files)
	}
	row, err := db.New(f.database).GetProjectAsset(context.Background(), db.GetProjectAssetParams{ProjectID: f.project, AssetID: first.Asset.ID})
	if err != nil {
		t.Fatal(err)
	}
	originalPath := f.path("/assets/" + first.Asset.ID.String())
	f.json(t, f.owner, "GET", originalPath+"/thumbnail", nil, 409)
	f.request(t, actor{}, "GET", originalPath, nil, "", 401)
	f.request(t, f.outsider, "GET", originalPath, nil, "", 404)
	f.request(t, f.owner, "GET", f.path("/assets/"+uuid.NewString()), nil, "", 404)
	f.upload(t, f.owner, f.project, firstShot, []byte("not an image"), 400)
	f.upload(t, f.owner, f.project, firstShot, bytes.Repeat([]byte("x"), 1025), 413)
	f.upload(t, f.outsider, f.project, firstShot, raw, 404)
	// New worker instance claims the persisted job and publishes only ready metadata.
	restarted := assets.NewService(f.database, f.storage, f.operations, f.assets.Limits)
	ready := false
	restarted.Publish = func(_ uuid.UUID, asset assets.Asset) { ready = asset.Status == "ready" }
	if ok, err := restarted.ProcessOne(context.Background()); !ok || err != nil || !ready {
		t.Fatalf("persistent thumbnail processing: %v %v", ok, err)
	}
	thumbnail := f.request(t, f.editor, "GET", originalPath+"/thumbnail", nil, "", 200)
	config, format, err := image.DecodeConfig(bytes.NewReader(thumbnail))
	if err != nil || format != "jpeg" || config.Width > 320 || config.Height > 320 {
		t.Fatalf("invalid thumbnail: %v", err)
	}
	// Delete one shot through the real operation endpoint. Its sibling still owns
	// the shared reference; deleting that shot must not unlink the original blob.
	f.submit(t, f.owner, operation("shot.delete", map[string]any{"scene_id": scene, "shot_id": firstShot}))
	state := decode[board.State](t, f.json(t, f.owner, "GET", f.path("/board"), nil, 200))
	if len(state.Scenes[0].Shots) != 1 || len(state.Scenes[0].Shots[0].AssetIDs) != 1 || state.Scenes[0].Shots[0].AssetIDs[0] != first.Asset.ID {
		t.Fatal("deleting one shot lost the sibling attachment")
	}
	if err := f.database.QueryRow(`SELECT COUNT(*) FROM shot_assets WHERE asset_id=$1`, first.Asset.ID).Scan(&references); err != nil || references != 1 {
		t.Fatalf("shared reference count: %d %v", references, err)
	}
	if _, err = os.Stat(filepath.Join(f.storage.Root, row.StorageKey)); err != nil {
		t.Fatal("shared blob was deleted")
	}
	if download := f.request(t, f.editor, "GET", originalPath, nil, "", 200); !bytes.Equal(download, raw) {
		t.Fatal("protected download changed original bytes")
	}
	// Identical bytes in a different project are independently scoped.
	other := f.newProject(t, "Other project")
	otherScene, otherShot := uuid.New(), uuid.New()
	for _, request := range []struct {
		kind    string
		payload any
	}{{"scene.create", map[string]any{"scene_id": otherScene, "title": "Other"}}, {"shot.create", map[string]any{"scene_id": otherScene, "shot_id": otherShot}}} {
		f.json(t, f.owner, "POST", "/api/projects/"+other.String()+"/ops", operation(request.kind, request.payload), 201)
	}
	otherUpload := decode[assets.UploadResult](t, f.upload(t, f.owner, other, otherShot, raw, 201))
	if otherUpload.Deduped || otherUpload.Asset.ID == first.Asset.ID {
		t.Fatal("cross-project dedup leaked a shared reference")
	}
	f.request(t, f.owner, "GET", "/api/projects/"+other.String()+"/assets/"+first.Asset.ID.String(), nil, "", 404)
	f.upload(t, f.owner, f.project, otherShot, raw, 404)
	// Both internal storage keys are hidden from HTTP JSON responses.
	uploadJSON := string(f.upload(t, f.owner, f.project, secondShot, raw, 201))
	if strings.Contains(uploadJSON, "storage_key") || strings.Contains(uploadJSON, row.StorageKey) {
		t.Fatal("storage key exposed")
	}
	file, err := f.storage.Open(row.StorageKey)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := io.ReadAll(file)
	file.Close()
	if err != nil || !bytes.Equal(stored, raw) {
		t.Fatal("shared blob bytes were lost")
	}
}
