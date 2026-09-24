package receipt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sample() *Receipt {
	return &Receipt{
		Schema:     Schema,
		InstallID:  "gi_abc123",
		Timestamp:  "2026-01-01T00:00:00Z",
		Source:     "path:/tmp/widget",
		SourceID:   "local:/tmp/widget@deadbeef",
		CommitSHA:  "deadbeef",
		Goal:       "run the widget CLI",
		Strategy:   "cli_bridge",
		Support:    "supported",
		Capability: "widget.cli",
		FilesCreated: []FileChange{
			{Path: "manifests/widget.cli.json", Owner: "grokinstall"},
			{Path: "adapters/widget.cli/index.json", Owner: "grokinstall"},
		},
		CommandsExecuted:  []Command{{Binary: "widget", Args: []string{"--version"}}},
		DependenciesAdded: []string{},
		Verification: Verification{
			Performed: true,
			Passed:    true,
			Checks:    []Check{{Name: "command launches", Passed: true, Detail: "exited 0"}},
		},
		Result: "installed",
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	r := sample()
	dir := t.TempDir()
	path, err := r.Save(dir)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if back.InstallID != r.InstallID || back.Strategy != r.Strategy {
		t.Fatalf("round trip lost data: %+v", back)
	}
	if len(back.FilesCreated) != 2 {
		t.Fatalf("file changes lost: %+v", back.FilesCreated)
	}
	if !back.Verification.Passed {
		t.Fatal("verification state lost")
	}
}

func TestReceiptRecordsRequiredSections(t *testing.T) {
	data := mustJSON(t, sample())
	for _, key := range []string{
		"install_id", "timestamp", "source", "goal", "strategy",
		"files_created", "commands_executed", "dependencies_introduced",
		"verification", "result",
	} {
		if !strings.Contains(data, `"`+key+`"`) {
			t.Fatalf("receipt missing %q:\n%s", key, data)
		}
	}
}

func mustJSON(t *testing.T, r *Receipt) string {
	t.Helper()
	data, err := r.JSON()
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestValidateRejectsMissingInstallID(t *testing.T) {
	r := sample()
	r.InstallID = ""
	if err := r.Validate(); err == nil {
		t.Fatal("install id is required")
	}
}

func TestValidateRejectsMissingSource(t *testing.T) {
	r := sample()
	r.Source = ""
	if err := r.Validate(); err == nil {
		t.Fatal("source is required")
	}
}

func TestValidateAcceptsSample(t *testing.T) {
	if err := sample().Validate(); err != nil {
		t.Fatalf("sample receipt should validate: %v", err)
	}
}

func TestFailedVerificationCannotClaimSuccess(t *testing.T) {
	r := sample()
	r.Verification.Passed = false
	r.Result = "installed"
	if err := r.Validate(); err == nil {
		t.Fatal("a receipt must not claim success when verification failed")
	}
}

func TestNoInstallResultIsValid(t *testing.T) {
	r := sample()
	r.Strategy = "no_install"
	r.Result = "no_install"
	r.Verification = Verification{Performed: false}
	r.FilesCreated = nil
	if err := r.Validate(); err != nil {
		t.Fatalf("a no-install receipt is a successful outcome: %v", err)
	}
}

func TestLoadMissingReceipt(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("expected an error")
	}
}

func TestLoadCorruptReceipt(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(p, []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("expected a parse error")
	}
}

func TestOwnerIsRecordedPerFile(t *testing.T) {
	r := sample()
	r.FilesCreated = append(r.FilesCreated, FileChange{Path: "some/upstream/file", Owner: "upstream"})
	data := mustJSON(t, r)
	if !strings.Contains(data, `"owner": "upstream"`) {
		t.Fatalf("ownership must be recorded so uninstall never guesses:\n%s", data)
	}
}

func TestCommandsAreRecordedWithArgv(t *testing.T) {
	data := mustJSON(t, sample())
	if !strings.Contains(data, `"binary"`) || !strings.Contains(data, `"args"`) {
		t.Fatalf("commands must be recorded with explicit argv:\n%s", data)
	}
}
