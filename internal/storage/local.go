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

// BlobStore stores and fetches image bytes by key. Keys are content-addressed (sha256), so
// Put is idempotent: if the key already exists it succeeds with created=false and leaves the
// existing blob untouched. Callers only Delete blobs they created.
type BlobStore interface {
	Put(ctx context.Context, key string, data []byte) (created bool, err error)
	Get(ctx context.Context, key string) ([]byte, error)
	Delete(ctx context.Context, key string) error
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

// Put writes to a unique temp file in the target directory, then hard-links it into place.
// os.Link fails with "exists" instead of overwriting, so concurrent writers of the same key
// never observe a partial file and exactly one reports created=true.
func (l *Local) Put(_ context.Context, key string, data []byte) (bool, error) {
	p, err := l.path(key)
	if err != nil {
		return false, err
	}
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return false, err
	}
	if _, err := os.Stat(p); err == nil {
		return false, nil
	}
	f, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return false, err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return false, err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return false, err
	}
	if err := f.Close(); err != nil {
		return false, err
	}
	if err := os.Chmod(tmp, 0o640); err != nil {
		return false, err
	}
	if err := os.Link(tmp, p); err != nil {
		if errors.Is(err, os.ErrExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (l *Local) Delete(_ context.Context, key string) error {
	p, err := l.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
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
func (m *Memory) Put(_ context.Context, k string, d []byte) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.M[k]; ok {
		return false, nil
	}
	m.M[k] = append([]byte(nil), d...)
	return true, nil
}
func (m *Memory) Delete(_ context.Context, k string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.M, k)
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
