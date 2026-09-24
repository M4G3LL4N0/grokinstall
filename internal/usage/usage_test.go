package usage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecordAndAggregate(t *testing.T) {
	dir := t.TempDir()
	if err := Record(dir, Event{Op: OpInspect, Source: "path:/a", CacheHit: false}); err != nil {
		t.Fatal(err)
	}
	if err := Record(dir, Event{Op: OpInspect, Source: "path:/a", CacheHit: true}); err != nil {
		t.Fatal(err)
	}
	if err := Record(dir, Event{Op: OpPlan, Source: "path:/b"}); err != nil {
		t.Fatal(err)
	}
	sum, err := Aggregate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if sum.TotalEvents != 3 {
		t.Fatalf("total = %d, want 3", sum.TotalEvents)
	}
	if sum.ByOp[OpInspect] != 2 {
		t.Fatalf("inspect events = %d", sum.ByOp[OpInspect])
	}
	if sum.CacheHits != 1 || sum.CacheMisses != 2 {
		t.Fatalf("cache hits/misses = %d/%d, want 1/2", sum.CacheHits, sum.CacheMisses)
	}
}

func TestUsageIsJSONL(t *testing.T) {
	dir := t.TempDir()
	_ = Record(dir, Event{Op: OpInspect, Source: "path:/a"})
	data, err := os.ReadFile(filepath.Join(dir, "usage.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected one JSONL line, got %d", len(lines))
	}
	if !strings.HasPrefix(lines[0], "{") {
		t.Fatalf("usage line is not JSON: %s", lines[0])
	}
}

func TestAggregateIgnoresCorruptLines(t *testing.T) {
	dir := t.TempDir()
	_ = Record(dir, Event{Op: OpInspect, Source: "path:/a"})
	f, err := os.OpenFile(filepath.Join(dir, "usage.jsonl"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("not json\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	sum, err := Aggregate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if sum.TotalEvents != 1 {
		t.Fatalf("corrupt line should be skipped, got %d events", sum.TotalEvents)
	}
}

func TestAggregateEmptyDir(t *testing.T) {
	sum, err := Aggregate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if sum.TotalEvents != 0 {
		t.Fatalf("total = %d, want 0", sum.TotalEvents)
	}
}

func TestContextPackSizeIsMeasuredNotEstimated(t *testing.T) {
	dir := t.TempDir()
	_ = Record(dir, Event{Op: OpPlan, Source: "path:/a", ContextPackBytes: 2048, GrokBotContractBytes: 0})
	sum, err := Aggregate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if sum.ContextPackBytes != 2048 {
		t.Fatalf("context pack bytes = %d, want 2048", sum.ContextPackBytes)
	}
}
