package installer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/M4G3LL4N0/grokinstall/internal/manifest"
	"github.com/M4G3LL4N0/grokinstall/internal/provision"
	"github.com/M4G3LL4N0/grokinstall/internal/registry"
	"github.com/M4G3LL4N0/grokinstall/internal/runtime"
)

// These tests pin the two flaws found by independent acceptance testing of
// v0.1.1. They are regression tests first: on the unfixed code they fail, and
// they describe the behaviour the fix must deliver.
//
//   Critical: `run` executed a capability whose GrokInstall-owned runtime had
//             been modified after installation. audit reported CRITICAL and
//             diagnose reported the modified runtime, but run still spawned the
//             tampered binary.
//   High:     cli_bridge wrote the caller's JSON to the subprocess stdin and
//             returned whatever came back. A capability named `review` could not
//             review anything, because nothing translated a path into argv.

// argvFixture is a CLI that only works when it receives real arguments. It
// echoes the arguments it was given and writes a marker file, so a test can
// distinguish "argv was translated" from "stdin was piped".
func argvFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write(t, dir, "README.md", "# argvtool\n\nShows a file.\n")
	write(t, dir, "package.json", `{"name":"argvtool","version":"1.0.0","bin":{"argvtool":"bin/argvtool.sh"}}`)
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Deliberately ignores stdin. A pipe-to-stdin capability cannot drive this,
	// which is exactly the flaw the adapter model exists to close.
	body := `#!/bin/sh
if [ "$1" = "--help" ] || [ "$1" = "-h" ]; then
  printf 'argvtool - display a file\n\nUSAGE:\n  argvtool [OPTIONS] <FILE>\n\nOPTIONS:\n  --style <name>  output style\n'
  exit 0
fi
style=plain
while [ $# -gt 0 ]; do
  case "$1" in
    --style) style="$2"; shift 2 ;;
    --) shift; break ;;
    -*) shift ;;
    *) break ;;
  esac
done
printf 'STYLE=%s\n' "$style"
printf 'FILE CONTENT: %s\n' "$(cat "$1" 2>/dev/null)"
`
	if err := os.WriteFile(filepath.Join(dir, "bin", "argvtool.sh"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func installArgvTool(t *testing.T, name string) (*Installer, string) {
	t.Helper()
	dir := argvFixture(t)
	ins := newInstaller(t)
	res, err := ins.Install(Options{Source: localSource(t, dir), Name: name, Goal: "let GrokBot use argvtool to show a file"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Result != "installed" {
		t.Fatalf("install result = %q: %+v", res.Result, res)
	}
	return ins, dir
}

func manifestOf(t *testing.T, ins *Installer, name string) *manifest.Manifest {
	t.Helper()
	entry, err := ins.Registry.Lookup(name)
	if err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Load(entry.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// --- Critical regression: a tampered runtime must not execute ----------------

// installOwnedRuntime registers a capability backed by a genuine
// GrokInstall-owned runtime with recorded hashes. That ownership is the whole
// precondition for the trust gate: a CLI that merely happens to sit in a source
// tree is the user's file, not something GrokInstall can promise the bytes of.
func installOwnedRuntime(t *testing.T, name string) (*Installer, string, string) {
	t.Helper()
	ins := newInstaller(t)
	dir := t.TempDir()
	binDir := filepath.Join(dir, "runtimes", name, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	binPath := filepath.Join(binDir, "tool")
	body := "#!/bin/sh\nprintf 'STYLE=plain\\n'\nprintf 'TOOL RAN\\n'\n"
	if err := os.WriteFile(binPath, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	runtimeDir := filepath.Join(dir, "runtimes", name)
	sum, err := hashOf(binPath)
	if err != nil {
		t.Fatal(err)
	}
	meta := provision.Metadata{
		Schema:     provision.MetadataSchema,
		Capability: name,
		Source:     "https://github.com/example/tool",
		Method:     provision.MethodRelease,
		CreatedAt:  time.Now(),
		Executable: "bin/tool",
		Checksums:  map[string]string{"bin/tool": sum},
		Ownership:  provision.OwnershipGrokinstall,
	}
	metaJSON, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, "metadata.json"), metaJSON, 0o644); err != nil {
		t.Fatal(err)
	}

	m := &manifest.Manifest{
		Schema:   manifest.CurrentSchema,
		Name:     name,
		Version:  "1",
		Source:   "https://github.com/example/tool",
		Goal:     "let GrokBot use tool to show a file",
		Strategy: "cli_bridge",
		Support:  manifest.SupportReady,
		Status:   "ready",
		Execution: manifest.Execution{
			Type:           manifest.ExecutionSubprocess,
			Supported:      true,
			Command:        binPath,
			TimeoutMs:      30000,
			MaxOutputBytes: 1 << 20,
			Ownership:      manifest.OwnershipGrokinstall,
		},
		Adapter: &manifest.Adapter{
			Kind: "cli", OutputMode: "text", OutputField: "text", Operation: "view",
			Confidence: "high", Evidence: []string{"test fixture: owned runtime"},
			Argv: []manifest.Binding{
				{From: "style", Kind: "flag", Flag: "--style"},
				{From: "path", Kind: "positional", Position: 0, Required: true},
			},
		},
		Input:    manifest.Schema{Fields: []manifest.Field{{Name: "path", Type: "string", Required: true}}},
		Output:   manifest.Schema{Fields: []manifest.Field{{Name: "text", Type: "string"}}},
		Grokbot:  manifest.Grokbot{UseWhen: "show a file"},
		Security: manifest.Security{OwnedByGrokinstall: true},
	}
	manifestPath, err := m.Save(ins.Registry.ManifestsDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := ins.Registry.Register(registry.Entry{
		Name:         name,
		Strategy:     "cli_bridge",
		Support:      string(manifest.SupportReady),
		State:        registry.StateReady,
		Source:       m.Source,
		ManifestPath: manifestPath,
		RuntimeDir:   runtimeDir,
		InstallID:    "gi_trust",
	}); err != nil {
		t.Fatal(err)
	}
	return ins, runtimeDir, binPath
}

// hashOf is a local SHA-256 helper so the fixture records the same kind of
// baseline the provisioner does.
func hashOf(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func TestRunIsBlockedWhenOwnedRuntimeWasTampered(t *testing.T) {
	ins, _, binPath := installOwnedRuntime(t, "owned.view")
	target := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(target, []byte("content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	input := json.RawMessage(`{"path":"` + target + `"}`)

	// Sanity: it runs before tampering.
	if res, err := ins.Run("owned.view", input); err != nil {
		t.Fatal(err)
	} else if !res.OK {
		t.Fatalf("clean capability should run, got %+v", res.Error)
	}

	// Tamper exactly as an attacker or an accident would.
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\nprintf 'TAMPERED'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	res, err := ins.Run("owned.view", input)
	if err != nil {
		t.Fatal(err)
	}
	if res.OK {
		t.Fatal("run executed a capability whose GrokInstall-owned runtime was modified after installation")
	}
	if res.Error == nil {
		t.Fatal("blocked execution must return a structured error")
	}
	if res.Error.Code != runtime.CodeIntegrityFailure {
		t.Fatalf("error code = %q, want %q", res.Error.Code, runtime.CodeIntegrityFailure)
	}
	if res.Error.Capability != "owned.view" || res.Error.Component != "runtime" {
		t.Fatalf("error must name the capability and component: %+v", res.Error)
	}
	if res.Error.RecommendedAction == "" {
		t.Fatal("a blocked execution must tell the caller what to do next")
	}
}

func TestRunIsBlockedWhenOwnedRuntimeWasDeleted(t *testing.T) {
	ins, runtimeDir, _ := installOwnedRuntime(t, "owned.view")
	target := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(target, []byte("content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(runtimeDir, "bin")); err != nil {
		t.Fatal(err)
	}
	res, err := ins.Run("owned.view", json.RawMessage(`{"path":"`+target+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.OK {
		t.Fatal("run executed a capability whose GrokInstall-owned runtime was deleted")
	}
	if res.Error == nil || res.Error.Code == "" {
		t.Fatalf("blocked execution must return a structured error, got %+v", res.Error)
	}
}

// The gate must not repair anything: after a blocked run the tampered file has
// to still be there, byte for byte, because execution and repair are separate
// trust boundaries.
func TestBlockedRunDoesNotRepairTheRuntime(t *testing.T) {
	ins, _, binPath := installOwnedRuntime(t, "owned.view")
	target := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(target, []byte("content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bad := []byte("#!/bin/sh\nprintf 'TAMPERED'\n")
	if err := os.WriteFile(binPath, bad, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ins.Run("owned.view", json.RawMessage(`{"path":"`+target+`"}`)); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(bad) {
		t.Fatal("run silently repaired or replaced the modified executable; execution and repair must stay separate")
	}
}

// --- High regression: cli_bridge must translate input, not pipe JSON --------

func TestCLICapabilityTranslatesInputToArgv(t *testing.T) {
	ins, dir := installArgvTool(t, "argvtool.show")

	// A real file whose content we can assert on.
	target := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(target, []byte("real payload\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := manifestOf(t, ins, "argvtool.show")
	if m.Adapter == nil {
		t.Fatal("a runnable cli_bridge capability must carry an adapter; without one it can only pipe JSON to stdin")
	}

	res, err := ins.Run("argvtool.show", json.RawMessage(`{"path":"`+target+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("capability failed: %+v", res.Error)
	}

	// The upstream tool must have received the path as an argument, and the
	// result must contain the file's real content, not the request echoed back.
	encoded, err := json.Marshal(res.Result)
	if err != nil {
		t.Fatal(err)
	}
	out := string(encoded)
	if !strings.Contains(out, "real payload") {
		t.Fatalf("result does not contain the file's contents; the adapter did not invoke the CLI usefully: %s", out)
	}
	if strings.Contains(out, `"path"`) {
		t.Fatalf("result merely echoes the request: %s", out)
	}
}

// --- External executables must not be treated as owned ----------------------

func TestExternalExecutableIsNotBlockedByOwnedRuntimeRules(t *testing.T) {
	ins := newInstaller(t)
	// A pre-existing executable on this machine: GrokInstall does not own it.
	external := externalToolFixture(t)

	m := &manifest.Manifest{
		Schema:   manifest.CurrentSchema,
		Name:     "external.run",
		Source:   "path:" + external,
		Goal:     "let GrokBot use the pre-installed tool",
		Strategy: "cli_bridge",
		Support:  manifest.SupportReady,
		Status:   "ready",
		Execution: manifest.Execution{
			Type:      manifest.ExecutionSubprocess,
			Supported: true,
			Command:   external,
			Args:      []string{"--version"},
			Ownership: manifest.OwnershipExternal,
		},
		Adapter: &manifest.Adapter{
			Kind: "cli", OutputMode: "text", OutputField: "text", Operation: "run",
			Confidence: "high", Evidence: []string{"test fixture"},
			Argv: []manifest.Binding{{From: "path", Kind: "positional", Position: 0}},
		},
		Input:  manifest.Schema{Fields: []manifest.Field{{Name: "path", Type: "string"}}},
		Output: manifest.Schema{Fields: []manifest.Field{{Name: "text", Type: "string"}}},
	}
	if _, err := m.Save(ins.Registry.ManifestsDir()); err != nil {
		t.Fatal(err)
	}
	if err := ins.Registry.Register(registry.Entry{
		Name:         m.Name,
		ManifestPath: filepath.Join(ins.Registry.ManifestsDir(), m.Name+".json"),
		Support:      string(m.Support),
		State:        "ready",
	}); err != nil {
		t.Fatal(err)
	}

	res, err := ins.Run(m.Name, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("an external executable with no recorded baseline must still run: %+v", res.Error)
	}
}

// externalToolFixture writes a trivial executable outside any GrokInstall-owned
// runtime directory and returns its path.
func externalToolFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "external-tool")
	body := "#!/bin/sh\nprintf 'external v1\\n'\n"
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}
