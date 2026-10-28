// Package storage holds uploaded receipt images. Local disk for now; an object store
// (S3-compatible) implementation lands with the Kubernetes phase.
package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// BlobStore stores and fetches image bytes by key.
type BlobStore interface {
	Put(ctx context.Context, key string, data []byte) error
	Get(ctx context.Context, key string) ([]byte, error)
}

// Local stores blobs under Dir (UPLOAD_DIR).
type Local struct{ Dir string }

func NewLocal(dir string) (*Local, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("create upload dir: %w", err)
	}
	return &Local{Dir: dir}, nil
}

func (l *Local) path(key string) (string, error) {
	clean := filepath.Clean("/" + key)
	if strings.Contains(key, "..") || clean == "/" {
		return "", errors.New("invalid blob key")
	}
	return filepath.Join(l.Dir, clean), nil
}

func (l *Local) Put(_ context.Context, key string, data []byte) error {
	p, err := l.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	tmp := p + ".part"
	if err := os.WriteFile(tmp, data, 0o640); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func (l *Local) Get(_ context.Context, key string) ([]byte, error) {
	p, err := l.path(key)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(p)
}

// Memory is an in-memory BlobStore for tests.
type Memory struct {
	mu sync.Mutex
	M  map[string][]byte
}

func NewMemory() *Memory { return &Memory{M: map[string][]byte{}} }
func (m *Memory) Put(_ context.Context, k string, d []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.M[k] = append([]byte(nil), d...)
	return nil
}
func (m *Memory) Get(_ context.Context, k string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.M[k]
	if !ok {
		return nil, os.ErrNotExist
	}
	return d, nil
}
