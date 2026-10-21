package assets

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type Storage struct{ Root string }

func NewStorage(root string) (*Storage, error) {
	if root == "" {
		return nil, fmt.Errorf("asset storage root is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absolute, 0750); err != nil {
		return nil, err
	}
	return &Storage{Root: absolute}, nil
}
func (s *Storage) path(key string) (string, error) {
	if key == "" || key == "." || filepath.IsAbs(key) {
		return "", fmt.Errorf("invalid storage key")
	}
	path := filepath.Join(s.Root, filepath.Clean(key))
	relative, err := filepath.Rel(s.Root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("invalid storage key")
	}
	return path, nil
}
func (s *Storage) Put(key string, reader io.Reader) error {
	path, err := s.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".upload-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err := io.Copy(file, reader); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	// Link creates the immutable destination atomically and never overwrites it.
	if err := os.Link(file.Name(), path); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	return nil
}
func (s *Storage) Open(key string) (*os.File, error) {
	path, err := s.path(key)
	if err != nil {
		return nil, err
	}
	return os.Open(path)
}
