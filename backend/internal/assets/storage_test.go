package assets

import (
	"bytes"
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
