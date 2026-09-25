package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

// --- ownership policy ------------------------------------------------------

// A GrokInstall-owned runtime must match the hashes recorded at install time,
// or it does not run. This is the gate the audit found missing.
func TestVerifyBlocksModifiedOwnedRuntime(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "tool")
	if err := os.WriteFile(bin, []byte("original"), 0o755); err != nil {
		t.Fatal(err)
	}
	recorded := sha256Of(t, bin)
	if err := os.WriteFile(bin, []byte("tampered"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := Verify(Trust{
		Ownership:  OwnershipGrokinstall,
		Command:    bin,
		RuntimeDir: dir,
		Recorded:   map[string]string{"tool": recorded},
	}, "demo.view")
	if err == nil {
		t.Fatal("a modified owned runtime must be blocked")
	}
	if err.Code != CodeIntegrityFailure {
		t.Fatalf("code = %q, want %q", err.Code, CodeIntegrityFailure)
	}
	if err.Component != "runtime" || err.Capability != "demo.view" {
		t.Fatalf("error must name the capability and component: %+v", err)
	}
	if err.RecommendedAction != "grokinstall diagnose demo.view" {
		t.Fatalf("error must tell the caller what to do: %q", err.RecommendedAction)
	}
}

// The executable is present but another recorded file was removed: that is an
// integrity failure, distinct from the target being absent altogether.
func TestVerifyBlocksMissingOwnedRuntimeFile(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "tool")
	if err := os.WriteFile(bin, []byte("present"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := Verify(Trust{
		Ownership:  OwnershipGrokinstall,
		Command:    bin,
		RuntimeDir: dir,
		Recorded: map[string]string{
			"tool":        sha256Of(t, bin),
			"bin/removed": "deadbeef",
		},
	}, "demo.view")
	if err == nil {
		t.Fatal("a missing owned runtime file must be blocked")
	}
	if err.Code != CodeIntegrityFailure {
		t.Fatalf("code = %q, want %q", err.Code, CodeIntegrityFailure)
	}
}

func TestVerifyBlocksMissingExecutable(t *testing.T) {
	err := Verify(Trust{
		Ownership: OwnershipGrokinstall,
		Command:   filepath.Join(t.TempDir(), "absent"),
	}, "demo.view")
	if err == nil {
		t.Fatal("a missing execution target must be blocked")
	}
	if err.Code != CodeNotExecutable {
		t.Fatalf("code = %q, want %q", err.Code, CodeNotExecutable)
	}
}

// An owned runtime with no recorded baseline cannot be trusted. Refusing is the
// only safe answer: silently trusting it is the failure the gate prevents.
func TestVerifyRefusesOwnedRuntimeWithNoBaseline(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "tool")
	if err := os.WriteFile(bin, []byte("anything"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := Verify(Trust{
		Ownership:  OwnershipGrokinstall,
		Command:    bin,
		RuntimeDir: dir,
		Recorded:   nil,
	}, "demo.view")
	if err == nil {
		t.Fatal("an owned runtime with no recorded hashes must be refused")
	}
	if err.Code != CodeIntegrityFailure {
		t.Fatalf("code = %q, want %q", err.Code, CodeIntegrityFailure)
	}
}

func TestVerifyAllowsCleanOwnedRuntime(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "tool")
	if err := os.WriteFile(bin, []byte("original"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := Verify(Trust{
		Ownership:  OwnershipGrokinstall,
		Command:    bin,
		RuntimeDir: dir,
		Recorded:   map[string]string{"tool": sha256Of(t, bin)},
	}, "demo.view")
	if err != nil {
		t.Fatalf("a clean owned runtime must be allowed to run: %+v", err)
	}
}

// The policy that keeps the tool honest: a normal package upgrade of an
// external tool changes the file, and GrokInstall must not report that as
// tampering it could not have caused.
func TestVerifyAllowsChangedExternalExecutable(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "jq")
	if err := os.WriteFile(bin, []byte("v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A stale baseline exists, because a baseline was recorded at install time.
	trust := Trust{
		Ownership:  OwnershipExternal,
		Command:    bin,
		RuntimeDir: dir,
		Recorded:   map[string]string{"jq": "an-old-hash"},
	}
	if err := Verify(trust, "jq.filter"); err != nil {
		t.Fatalf("an upgraded external tool must not read as tampering: %+v", err)
	}
}

func TestVerifyAllowsExternalWithNoBaseline(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "jq")
	if err := os.WriteFile(bin, []byte("v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Verify(Trust{Ownership: OwnershipExternal, Command: bin}, "jq.filter"); err != nil {
		t.Fatalf("an external executable with no baseline must still run: %+v", err)
	}
}

func TestVerifyStillRequiresExternalExecutableToExist(t *testing.T) {
	err := Verify(Trust{
		Ownership: OwnershipExternal,
		Command:   filepath.Join(t.TempDir(), "absent"),
	}, "jq.filter")
	if err == nil {
		t.Fatal("an external target that does not exist must still be refused")
	}
	if err.Code != CodeNotExecutable {
		t.Fatalf("code = %q, want %q", err.Code, CodeNotExecutable)
	}
}

func TestVerifyUserSuppliedExecutableIsNotHashEnforced(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "mytool")
	if err := os.WriteFile(bin, []byte("v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Verify(Trust{
		Ownership: OwnershipSupplied,
		Command:   bin,
		Recorded:  map[string]string{"mytool": "stale"},
	}, "mine.run"); err != nil {
		t.Fatalf("a user-supplied executable is the user's to manage: %+v", err)
	}
}

func TestVerifyRejectsEmptyCommand(t *testing.T) {
	err := Verify(Trust{Ownership: OwnershipGrokinstall}, "demo.view")
	if err == nil {
		t.Fatal("an empty command must be refused")
	}
	if err.Code != CodeNotExecutable {
		t.Fatalf("code = %q, want %q", err.Code, CodeNotExecutable)
	}
}

// sha256Of hashes a file the same way the default hasher does, so recorded
// baselines in tests are realistic.
func sha256Of(t *testing.T, path string) string {
	t.Helper()
	sum, err := defaultHasher(path)
	if err != nil {
		t.Fatal(err)
	}
	return sum
}
