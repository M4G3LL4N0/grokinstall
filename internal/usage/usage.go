// Package usage records what GrokInstall actually did, in measured terms only.
// It never estimates token savings or invents provider cost.
package usage

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Operations recorded in the usage log.
const (
	OpInspect      = "inspect"
	OpPlan         = "plan"
	OpCompare      = "compare"
	OpDoctor       = "doctor"
	OpInstall      = "install"
	OpRun          = "run"
	OpTest         = "test"
	OpUninstall    = "uninstall"
	OpDiagnose     = "diagnose"
	OpAudit        = "audit"
	OpProvision    = "provision"
	OpGrokBot      = "grokbot"
	OpCapabilities = "capabilities"
	OpCacheHit     = "cache_hit"
	OpCacheMiss    = "cache_miss"
	OpWorker       = "worker"
	OpLocalOp      = "local_operation"
	OpContextPack  = "context_pack"
	OpGrokBotCall  = "grokbot_contract"
)

// Event is one recorded operation.
type Event struct {
	Time                  time.Time `json:"time"`
	Op                    string    `json:"op"`
	Source                string    `json:"source,omitempty"`
	Capability            string    `json:"capability,omitempty"`
	CacheHit              bool      `json:"cache_hit,omitempty"`
	Strategy              string    `json:"strategy,omitempty"`
	Worker                string    `json:"worker,omitempty"`
	ContextPackBytes      int       `json:"context_pack_bytes,omitempty"`
	GrokBotContractBytes  int       `json:"grokbot_contract_bytes,omitempty"`
	CapabilityInputBytes  int       `json:"capability_input_bytes,omitempty"`
	CapabilityOutputBytes int       `json:"capability_output_bytes,omitempty"`
	AdapterExecutions     int       `json:"adapter_executions,omitempty"`
	DurationMillis        int64     `json:"duration_ms,omitempty"`
	Outcome               string    `json:"outcome,omitempty"`
	Detail                string    `json:"detail,omitempty"`
}

// Summary aggregates the usage log. Every value is measured; byte counts are
// never presented as token counts.
type Summary struct {
	TotalEvents           int            `json:"total_events"`
	ByOp                  map[string]int `json:"by_op"`
	Installs              int            `json:"installs"`
	Runs                  int            `json:"runs"`
	Tests                 int            `json:"tests"`
	Uninstalls            int            `json:"uninstalls"`
	CacheHits             int            `json:"cache_hits"`
	CacheMisses           int            `json:"cache_misses"`
	WorkerInvocations     int            `json:"worker_invocations"`
	WorkerTypes           map[string]int `json:"worker_types,omitempty"`
	ContextPackBytes      int            `json:"context_pack_bytes"`
	GrokBotContractBytes  int            `json:"grokbot_contract_bytes"`
	CapabilityInputBytes  int            `json:"capability_input_bytes"`
	CapabilityOutputBytes int            `json:"capability_output_bytes"`
	AdapterExecutions     int            `json:"adapter_executions"`
	FirstEvent            *time.Time     `json:"first_event,omitempty"`
	LastEvent             *time.Time     `json:"last_event,omitempty"`
}

// Record appends an event to usage.jsonl in dir. Writes are line-atomic.
func Record(dir string, ev Event) error {
	if ev.Time.IsZero() {
		ev.Time = time.Now().UTC()
	}
	line, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "usage.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	mu.Lock()
	defer mu.Unlock()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

var mu sync.Mutex

// Aggregate reads the usage log and summarizes it. Corrupt lines are skipped
// rather than failing the whole report.
func Aggregate(dir string) (*Summary, error) {
	sum := &Summary{ByOp: map[string]int{}, WorkerTypes: map[string]int{}}
	f, err := os.Open(filepath.Join(dir, "usage.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return sum, nil
		}
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev Event
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		sum.TotalEvents++
		sum.ByOp[ev.Op]++
		switch ev.Op {
		case OpInstall:
			sum.Installs++
		case OpRun:
			sum.Runs++
		case OpTest:
			sum.Tests++
		case OpUninstall:
			sum.Uninstalls++
		case OpCacheHit:
			sum.CacheHits++
		case OpCacheMiss:
			sum.CacheMisses++
		case OpWorker:
			sum.WorkerInvocations++
			if ev.Worker != "" {
				sum.WorkerTypes[ev.Worker]++
			}
		}
		// An inspect/plan/compare/install run always consults the cache, so its
		// CacheHit field records the outcome of that lookup.
		if ev.Op == OpInspect || ev.Op == OpPlan || ev.Op == OpCompare || ev.Op == OpInstall {
			if ev.CacheHit {
				sum.CacheHits++
			} else {
				sum.CacheMisses++
			}
		}
		sum.ContextPackBytes += ev.ContextPackBytes
		sum.GrokBotContractBytes += ev.GrokBotContractBytes
		sum.CapabilityInputBytes += ev.CapabilityInputBytes
		sum.CapabilityOutputBytes += ev.CapabilityOutputBytes
		sum.AdapterExecutions += ev.AdapterExecutions
		t := ev.Time
		if sum.FirstEvent == nil || t.Before(*sum.FirstEvent) {
			tt := t
			sum.FirstEvent = &tt
		}
		if sum.LastEvent == nil || t.After(*sum.LastEvent) {
			tt := t
			sum.LastEvent = &tt
		}
	}
	return sum, scanner.Err()
}
