package cache

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewCreatesStateLayout(t *testing.T) {
	root := t.TempDir()
	s, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"config.json", "registry.json", "manifests", "adapters", "receipts", "cache", "diagnostics", "logs"} {
		if _, err := os.Stat(filepath.Join(s.Root, d)); err != nil {
			t.Fatalf("missing state path %s: %v", d, err)
		}
	}
}

func TestPutGetMissHit(t *testing.T) {
	s, _ := New(t.TempDir())
	k := Key{Namespace: "inspection", Identity: "local:/x@abc", Version: "v1"}

	if _, hit, err := s.Get(k); err != nil || hit {
		t.Fatalf("expected miss, got hit=%v err=%v", hit, err)
	}
	if err := s.Put(k, []byte(`{"x":1}`)); err != nil {
		t.Fatal(err)
	}
	data, hit, err := s.Get(k)
	if err != nil || !hit {
		t.Fatalf("expected hit, got hit=%v err=%v", hit, err)
	}
	if string(data) != `{"x":1}` {
		t.Fatalf("round trip mismatch: %s", data)
	}
}

func TestDifferentIdentitiesDoNotCollide(t *testing.T) {
	s, _ := New(t.TempDir())
	if err := s.Put(Key{Namespace: "inspection", Identity: "a", Version: "v1"}, []byte("A")); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(Key{Namespace: "inspection", Identity: "b", Version: "v1"}, []byte("B")); err != nil {
		t.Fatal(err)
	}
	got, hit, _ := s.Get(Key{Namespace: "inspection", Identity: "a", Version: "v1"})
	if !hit || string(got) != "A" {
		t.Fatalf("collision: %s", got)
	}
}

func TestVersionIsolatesCache(t *testing.T) {
	s, _ := New(t.TempDir())
	_ = s.Put(Key{Namespace: "inspection", Identity: "a", Version: "v1"}, []byte("old"))
	_, hit, _ := s.Get(Key{Namespace: "inspection", Identity: "a", Version: "v2"})
	if hit {
		t.Fatal("version bump must invalidate the cache entry")
	}
}

func TestPutIsAtomic(t *testing.T) {
	root := t.TempDir()
	s, _ := New(root)
	k := Key{Namespace: "inspection", Identity: "atomic", Version: "v1"}
	if err := s.Put(k, []byte("payload")); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(s.CacheDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Fatalf("temporary file left behind: %s", e.Name())
		}
	}
}

func TestStatsCounters(t *testing.T) {
	s, _ := New(t.TempDir())
	s.RecordHit()
	s.RecordMiss()
	st := s.Stats()
	if st.Hits != 1 || st.Misses != 1 {
		t.Fatalf("stats = %+v", st)
	}
}
