// Package cache owns GrokInstall's on-disk state layout and the deterministic
// inspection cache. State is plain JSON/JSONL written atomically; there is no
// database to operate in Part 1.
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Schema is the cache/state schema identifier.
const Schema = "grokinstall/state/v1"

// Store is the root of the GrokInstall state directory.
type Store struct {
	Root string

	mu    sync.Mutex
	stats Stats
}

// Stats counts cache activity for the current process.
type Stats struct {
	Hits   int `json:"hits"`
	Misses int `json:"misses"`
}

// New prepares the state layout under root, creating any missing directory and
// seeding the canonical JSON state files when absent.
func New(root string) (*Store, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	s := &Store{Root: abs}
	dirs := []string{"", "manifests", "adapters", "receipts", "cache", "diagnostics", "logs"}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(s.Root, d), 0o755); err != nil {
			return nil, fmt.Errorf("create state dir %s: %w", d, err)
		}
	}
	seed := func(path string, v any) error {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		return WriteJSONAtomic(path, v)
	}
	if err := seed(s.ConfigPath(), map[string]any{"schema": Schema, "version": "v1"}); err != nil {
		return nil, err
	}
	if err := seed(s.RegistryPath(), map[string]any{"schema": Schema, "capabilities": []any{}}); err != nil {
		return nil, err
	}
	return s, nil
}

// Paths into the state directory.
func (s *Store) ConfigPath() string     { return filepath.Join(s.Root, "config.json") }
func (s *Store) RegistryPath() string   { return filepath.Join(s.Root, "registry.json") }
func (s *Store) CacheDir() string       { return filepath.Join(s.Root, "cache") }
func (s *Store) WorkDir() string        { return filepath.Join(s.Root, "cache", "work") }
func (s *Store) LogsDir() string        { return filepath.Join(s.Root, "logs") }
func (s *Store) DiagnosticsDir() string { return filepath.Join(s.Root, "diagnostics") }
func (s *Store) UsagePath() string      { return filepath.Join(s.Root, "logs", "usage.jsonl") }

// Key identifies a cached artifact. Version participates in the key so that a
// change in inspection logic invalidates old entries.
type Key struct {
	Namespace string `json:"namespace"`
	Identity  string `json:"identity"`
	Version   string `json:"version"`
}

// Hash is the stable key digest used for file names.
func (k Key) Hash() string {
	sum := sha256.Sum256([]byte(k.Namespace + "\x00" + k.Identity + "\x00" + k.Version))
	return hex.EncodeToString(sum[:])
}

func (s *Store) entryPath(k Key) string {
	return filepath.Join(s.CacheDir(), k.Namespace, k.Hash()+".json")
}

// Get returns a cached payload and whether it was present.
func (s *Store) Get(k Key) ([]byte, bool, error) {
	data, err := os.ReadFile(s.entryPath(k))
	if err != nil {
		if os.IsNotExist(err) {
			s.RecordMiss()
			return nil, false, nil
		}
		return nil, false, err
	}
	s.RecordHit()
	return data, true, nil
}

// Put stores a payload atomically.
func (s *Store) Put(k Key, data []byte) error {
	path := s.entryPath(k)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return WriteFileAtomic(path, data, 0o644)
}

// RecordHit increments the in-process hit counter.
func (s *Store) RecordHit() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats.Hits++
}

// RecordMiss increments the in-process miss counter.
func (s *Store) RecordMiss() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats.Misses++
}

// Stats returns a snapshot of cache counters.
func (s *Store) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}

// WriteFileAtomic writes data through a temporary file and a rename, so readers
// never observe a partial file.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// WriteJSONAtomic marshals v and writes it atomically.
func WriteJSONAtomic(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return WriteFileAtomic(path, data, 0o644)
}

// ListCacheEntries returns cache keys grouped by namespace, newest last.
func (s *Store) ListCacheEntries() (map[string]int, error) {
	out := map[string]int{}
	entries, err := os.ReadDir(s.CacheDir())
	if err != nil {
		return out, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		info, err := os.Stat(filepath.Join(s.CacheDir(), name))
		if err != nil || !info.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(s.CacheDir(), name))
		if err != nil {
			continue
		}
		out[name] = len(files)
	}
	return out, nil
}
