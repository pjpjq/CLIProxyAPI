package auth

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gofrs/flock"
)

type pooledFileSaveKey struct{}

// PooledFileSave captures a batch write under the same lock as the durable save.
type PooledFileSave struct {
	store                 *FileTokenStore
	activePath, vaultPath string
	wasActive             bool
	before, after         []byte
	readError             error
}

// ObservePooledFileSave records the next pooled save for conditional rollback.
func (s *FileTokenStore) ObservePooledFileSave(ctx context.Context) (context.Context, *PooledFileSave) {
	if s.poolDir == "" {
		return ctx, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	save := &PooledFileSave{store: s}
	return context.WithValue(ctx, pooledFileSaveKey{}, save), save
}

func (r *PooledFileSave) Captured() bool { return r != nil && r.vaultPath != "" }

func readPooledFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	return data, err
}

func (r *PooledFileSave) capture(activePath, vaultPath string) error {
	_, statErr := os.Lstat(activePath)
	if statErr != nil && !os.IsNotExist(statErr) {
		return statErr
	}
	before, err := readPooledFile(vaultPath)
	if err != nil {
		return err
	}
	r.activePath, r.vaultPath = activePath, vaultPath
	r.wasActive, r.before = statErr == nil, before
	return nil
}

// Rollback restores this exact write iff the vault has not since changed.
func (r *PooledFileSave) Rollback() (bool, error) {
	s := r.store
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := s.lockPool()
	if err != nil {
		return false, err
	}
	defer unlock()
	if r.readError != nil {
		return false, r.readError
	}
	current, err := readPooledFile(r.vaultPath)
	if err != nil {
		return false, err
	}
	if (current == nil) != (r.after == nil) || !bytes.Equal(current, r.after) {
		return false, nil
	}
	if r.before == nil {
		if err := os.Remove(r.vaultPath); err != nil && !os.IsNotExist(err) {
			return false, err
		}
	} else if current == nil || !bytes.Equal(r.before, current) {
		temp, err := os.CreateTemp(filepath.Dir(r.vaultPath), "."+filepath.Base(r.vaultPath)+".tmp-rollback-")
		if err != nil {
			return false, err
		}
		defer os.Remove(temp.Name())
		_, writeErr := temp.Write(r.before)
		closeErr := temp.Close()
		if writeErr != nil {
			return false, writeErr
		}
		if closeErr != nil {
			return false, closeErr
		}
		if err := os.Rename(temp.Name(), r.vaultPath); err != nil {
			return false, err
		}
	}
	if r.wasActive && r.before != nil {
		return true, s.publishPoolFile(r.vaultPath, r.activePath, true)
	}
	if err := os.Remove(r.activePath); err != nil && !os.IsNotExist(err) {
		return false, err
	}
	return true, nil
}

func (s *FileTokenStore) lockPool() (func(), error) {
	if s.poolDir == "" {
		return func() {}, nil
	}
	lock := flock.New(filepath.Join(s.poolDir, ".pool.lock"))
	if err := lock.Lock(); err != nil {
		return nil, fmt.Errorf("auth filestore: lock pool: %w", err)
	}
	return func() { _ = lock.Unlock() }, nil
}

// AuthoritativeAuthPath maps a projected credential to its vault backing file.
func (s *FileTokenStore) AuthoritativeAuthPath(path string) string {
	if s.poolDir != "" && filepath.Dir(filepath.Clean(path)) == s.poolDir &&
		strings.HasSuffix(strings.ToLower(path), ".json") {
		return filepath.Join(s.vaultDir, filepath.Base(path))
	}
	return path
}

// WriteManagedAuthFile writes uploads and source edits before publishing them in the projection.
func (s *FileTokenStore) WriteManagedAuthFile(path string, create bool, write func(string) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := s.lockPool()
	if err != nil {
		return err
	}
	defer unlock()
	vaultPath := s.AuthoritativeAuthPath(path)
	if vaultPath != path && !create {
		if _, err := os.Stat(vaultPath); err != nil {
			return err
		}
	}
	if err := write(vaultPath); err != nil {
		return err
	}
	return s.publishPoolFile(vaultPath, path, create)
}

func (s *FileTokenStore) publishPoolFile(vaultPath, activePath string, create bool) error {
	if vaultPath == activePath {
		return nil
	}
	active, err := os.Stat(activePath)
	if os.IsNotExist(err) && !create {
		// Refreshes that finish after eviction update the inventory without re-admitting the credential.
		return nil
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	vault, err := os.Stat(vaultPath)
	if err != nil {
		return err
	}
	if active != nil && os.SameFile(active, vault) {
		return nil
	}
	temp, err := os.MkdirTemp(filepath.Dir(activePath), ".pool-link-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	link := filepath.Join(temp, filepath.Base(activePath))
	if err := os.Link(vaultPath, link); err != nil {
		if err := os.Symlink(vaultPath, link); err != nil {
			return fmt.Errorf("auth filestore: project vault credential: %w", err)
		}
	}
	return os.Rename(link, activePath)
}
