package assets

import (
	"bytes"
	"image"
	"image/png"
	"io"
	"testing"
)

func TestStorageImmutableAndContained(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"", ".", "../escape", "/absolute", "a/../../escape"} {
		if err := storage.Put(key, bytes.NewBufferString("x")); err == nil {
			t.Fatalf("accepted %q", key)
		}
	}
	if err = storage.Put("originals/hash.png", bytes.NewBufferString("first")); err != nil {
		t.Fatal(err)
	}
	if err = storage.Put("originals/hash.png", bytes.NewBufferString("second")); err != nil {
		t.Fatal(err)
	}
	file, err := storage.Open("originals/hash.png")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	raw, _ := io.ReadAll(file)
	if string(raw) != "first" {
		t.Fatal("immutable blob overwritten")
	}
}

func TestConfiguredPixelLimit(t *testing.T) {
	var raw bytes.Buffer
	if err := png.Encode(&raw, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	service := NewService(nil, nil, nil, Limits{MaxUploadBytes: 1024, MaxPixels: 15})
	if _, _, err := service.decode(raw.Bytes()); err == nil {
		t.Fatal("configured pixel limit ignored")
	}
	service.Limits.MaxPixels = 16
	if _, _, err := service.decode(raw.Bytes()); err != nil {
		t.Fatalf("valid image rejected: %v", err)
	}
}
