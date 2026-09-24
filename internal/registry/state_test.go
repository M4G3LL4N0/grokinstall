package registry

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPlanOnlyIsNotAnInstalledState(t *testing.T) {
	if StatePlanOnly.Installed() {
		t.Fatal("a plan is not an installed capability")
	}
	r := newReg(t)
	if err := r.Register(Entry{Name: "x.one", State: StatePlanOnly, Source: "path:/x"}); err == nil {
		t.Fatal("registering a plan as an installed capability must be refused")
	}
}

func TestInstalledStates(t *testing.T) {
	for _, s := range []State{StateReady, StateBroken, StateDirty} {
		if !s.Installed() {
			t.Fatalf("%s should count as installed", s)
		}
	}
	if !StateReady.Runnable() {
		t.Fatal("ready must be runnable")
	}
	for _, s := range []State{StateBroken, StateDirty, StateUninstalled, StatePlanOnly} {
		if s.Runnable() {
			t.Fatalf("%s must not be offered as runnable", s)
		}
	}
}

func TestSetStateRecordsReason(t *testing.T) {
	r := newReg(t)
	_ = r.Register(Entry{Name: "a.one", State: StateReady, Source: "path:/x"})
	if err := r.SetState("a.one", StateDirty, "rollback incomplete"); err != nil {
		t.Fatal(err)
	}
	got, _ := r.Lookup("a.one")
	if got.State != StateDirty || got.DirtyReason != "rollback incomplete" {
		t.Fatalf("entry = %+v", got)
	}
}

func TestSetStateRejectsUnknown(t *testing.T) {
	r := newReg(t)
	_ = r.Register(Entry{Name: "a.one", State: StateReady, Source: "path:/x"})
	if err := r.SetState("a.one", "weird", ""); err == nil {
		t.Fatal("unknown states must be rejected")
	}
}

func TestPlansAreStoredOutsideTheRegistry(t *testing.T) {
	r := newReg(t)
	path, err := r.SavePlan(PlanEntry{PlanID: "plan_1", Source: "path:/x", Strategy: "api_bridge"})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != r.PlansDir() {
		t.Fatalf("plans must live in the plans directory, got %s", path)
	}
	entries, _ := r.List()
	if len(entries) != 0 {
		t.Fatal("saving a plan must not create a capability entry")
	}
	plans, err := r.Plans()
	if err != nil || len(plans) != 1 {
		t.Fatalf("plans = %+v err = %v", plans, err)
	}
	if plans[0].State != StatePlanOnly {
		t.Fatalf("plan state = %q, want plan_only", plans[0].State)
	}
}

func TestMigrationMovesPart2PlanOnlyEntriesOutOfTheRegistry(t *testing.T) {
	r := newReg(t)
	// Simulate a Part 2 registry file containing a plan-only entry.
	legacy := []Entry{
		{Name: "real.cli", State: "", Strategy: "cli_bridge", Source: "path:/real"},
		{Name: "api.plan", State: "plan_only", Strategy: "api_bridge", Source: "path:/api"},
	}
	if err := r.WriteRegistry(legacy); err != nil {
		t.Fatal(err)
	}
	report, err := r.Migrate()
	if err != nil {
		t.Fatal(err)
	}
	if report.Migrated != 1 {
		t.Fatalf("migrated = %d, want 1", report.Migrated)
	}
	entries, _ := r.List()
	if len(entries) != 1 || entries[0].Name != "real.cli" {
		t.Fatalf("registry after migration = %+v", entries)
	}
	if entries[0].State != StateReady {
		t.Fatalf("legacy entry should default to ready, got %q", entries[0].State)
	}
	if len(report.Details) == 0 {
		t.Fatal("a migration must explain what it did")
	}
}

func TestMigrationIsIdempotent(t *testing.T) {
	r := newReg(t)
	_ = r.WriteRegistry([]Entry{{Name: "a.one", Strategy: "cli_bridge", Source: "path:/a"}})
	if _, err := r.Migrate(); err != nil {
		t.Fatal(err)
	}
	report, err := r.Migrate()
	if err != nil {
		t.Fatal(err)
	}
	if report.Migrated != 0 {
		t.Fatal("a second migration should have nothing to do")
	}
	entries, _ := r.List()
	if len(entries) != 1 || entries[0].State != StateReady {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestMigrationRefusesToDestroyCorruptState(t *testing.T) {
	r := newReg(t)
	if err := os.WriteFile(r.RegistryPath(), []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Migrate(); err == nil {
		t.Fatal("migration must refuse to run on corrupt state")
	}
	data, _ := os.ReadFile(r.RegistryPath())
	if string(data) != "{broken" {
		t.Fatal("a failed migration must not rewrite registry state")
	}
}

func TestRuntimePathIsOwned(t *testing.T) {
	r := newReg(t)
	p := r.RuntimePath("demo.run")
	if filepath.Dir(p) != r.RuntimesDir() {
		t.Fatalf("runtime path escapes the owned runtimes directory: %s", p)
	}
}
