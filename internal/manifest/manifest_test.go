package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSchemaIdentifier(t *testing.T) {
	if SchemaID != "grokinstall/v1" {
		t.Fatalf("schema = %q, want grokinstall/v1", SchemaID)
	}
}

func TestSaveAndLoad(t *testing.T) {
	m := installed()
	dir := t.TempDir()
	path, err := m.Save(dir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != m.Name+".json" {
		t.Fatalf("manifest filename = %q", filepath.Base(path))
	}
	back, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if back.Name != m.Name || back.Goal != m.Goal {
		t.Fatalf("round trip lost data: %+v", back)
	}
}

func TestSaveRefusesInvalidManifest(t *testing.T) {
	m := installed()
	m.Name = "Invalid Name!"
	if _, err := m.Save(t.TempDir()); err == nil {
		t.Fatal("invalid manifests must not be written")
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("expected an error")
	}
}

func TestLoadCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broken.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected a parse error")
	}
}

func TestContractTextIsTheMachineContract(t *testing.T) {
	text := installed().ContractText()
	if !strings.Contains(text, "CAPABILITY") {
		t.Fatalf("contract shape changed:\n%s", text)
	}
}

func TestSupportExecutableSemantics(t *testing.T) {
	if !SupportReady.Executable() || !SupportExperimental.Executable() {
		t.Fatal("ready and experimental must be executable")
	}
	for _, s := range []Support{SupportPlanOnly, SupportUnsupported, SupportNone} {
		if s.Executable() {
			t.Fatalf("%s must not be executable", s)
		}
	}
}

func TestManifestCarriesNoSourceContents(t *testing.T) {
	m := installed()
	data, err := m.JSON()
	if err != nil {
		t.Fatal(err)
	}
	// A manifest may reference a source identity but must never embed the
	// upstream file listing or documentation.
	if strings.Contains(string(data), "\"files\"") || strings.Contains(string(data), "\"contents\"") {
		t.Fatalf("manifest embedded source material:\n%s", data)
	}
}
