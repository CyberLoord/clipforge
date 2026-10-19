package storage

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
)

var ErrTooLarge = errors.New("file exceeds upload limit")

type Storage interface {
	Save(context.Context, string, io.Reader, int64) (int64, error)
	Path(string) (string, error)
	Remove(string) error
}

type Local struct{ root string }

func New(root string) (*Local, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(root, 0750); err != nil {
		return nil, err
	}
	return &Local{root: root}, nil
}
func (s *Local) Path(key string) (string, error) {
	if !filepath.IsLocal(key) {
		return "", errors.New("invalid storage key")
	}
	return filepath.Join(s.root, key), nil
}
func (s *Local) Save(ctx context.Context, key string, r io.Reader, limit int64) (int64, error) {
	path, err := s.Path(key)
	if err != nil {
		return 0, err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return 0, err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".upload-*")
	if err != nil {
		return 0, err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	n, err := io.Copy(f, io.LimitReader(&contextReader{ctx, r}, limit+1))
	if err != nil {
		return n, err
	}
	if n > limit {
		return n, ErrTooLarge
	}
	if err = f.Sync(); err != nil {
		return n, err
	}
	if err = f.Close(); err != nil {
		return n, err
	}
	return n, os.Rename(f.Name(), path)
}
func (s *Local) Remove(key string) error {
	path, err := s.Path(key)
	if err != nil {
		return err
	}
	return os.Remove(path)
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *contextReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(b)
}
