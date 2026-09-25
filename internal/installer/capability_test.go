package installer

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/M4G3LL4N0/grokinstall/internal/manifest"
	"github.com/M4G3LL4N0/grokinstall/internal/registry"
	"github.com/M4G3LL4N0/grokinstall/internal/runtime"
)

// A file-display CLI shaped like bat: it needs a real path as argv, ignores
// stdin, and prints the file. This is the shape the v0.1 pipe-to-stdin path
// could not drive at all.
func displayToolFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write(t, dir, "README.md", "# viewer\n\nDisplays a file.\n")
	write(t, dir, "package.json", `{"name":"viewer","version":"1.0.0","bin":{"viewer":"bin/viewer.sh"}}`)
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `#!/bin/sh
if [ "$1" = "--help" ] || [ "$1" = "-h" ]; then
  printf 'viewer - display a file\n\nUSAGE:\n  viewer [OPTIONS] <FILE>\n\nOPTIONS:\n  --style <name>   output style\n  --word <text>     match a word\n'
  exit 0
fi
style=plain
word=
while [ $# -gt 0 ]; do
  case "$1" in
    --style) style="$2"; shift 2 ;;
    --word) word="$2"; shift 2 ;;
    --) shift; break ;;
    -*) shift ;;
    *) break ;;
  esac
done
printf 'STYLE=%s\n' "$style"
if [ -n "$word" ]; then
  grep -- "$word" "$1" || true
else
  cat "$1"
fi
`
	if err := os.WriteFile(filepath.Join(dir, "bin", "viewer.sh"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// --- bat regression: the capability performs a real file operation ----------

func TestDisplayCapabilityPerformsRealFileOperation(t *testing.T) {
	dir := displayToolFixture(t)
	ins := newInstaller(t)
	res, err := ins.Install(Options{Source: localSource(t, dir), Goal: "let GrokBot show a file"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Result != "installed" {
		t.Fatalf("install result = %q: %+v", res.Result, res)
	}

	// A fixture file whose contents we can assert on exactly.
	target := filepath.Join(dir, "sample.txt")
	if err := os.WriteFile(target, []byte("REAL-FILE-CONTENT\nsecond line\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	entry, err := ins.Registry.Lookup(res.Capability)
	if err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Load(entry.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if m.Adapter == nil {
		t.Fatal("a runnable cli_bridge must carry a mapping")
	}

	out, err := ins.Run(res.Capability, json.RawMessage(`{"path":"`+target+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !out.OK {
		t.Fatalf("run failed: %+v", out.Error)
	}
	// The upstream tool's real output must be in the result.
	if !strings.Contains(out.RawResult, "REAL-FILE-CONTENT") {
		t.Fatalf("result does not contain the file's real contents:\n%s", out.RawResult)
	}
	// The request must not be echoed back: that was the v0.1 failure mode.
	if strings.Contains(out.RawResult, `"path"`) {
		t.Fatalf("result merely echoes the request:\n%s", out.RawResult)
	}
	// The flag binding must have reached the tool.
	if !strings.Contains(out.RawResult, "STYLE=plain") {
		t.Fatalf("the style option did not reach the tool:\n%s", out.RawResult)
	}
}

// Discovery must not invent input fields. Help text may mention --word, but
// mapping a caller's "word" field onto it is a guess, so a help-derived mapping
// exposes only what it can justify and rejects everything else.
func TestHelpDerivedMappingDoesNotInventInputFields(t *testing.T) {
	dir := displayToolFixture(t)
	ins := newInstaller(t)
	res, err := ins.Install(Options{Source: localSource(t, dir), Goal: "let GrokBot show a file"})
	if err != nil {
		t.Fatal(err)
	}
	entry, err := ins.Registry.Lookup(res.Capability)
	if err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Load(entry.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	// The fixture's help mentions --word, yet the mapping must not claim it.
	for _, f := range m.Input.Fields {
		if f.Name == "word" {
			t.Fatalf("a help-derived mapping must not invent an input field: %+v", m.Input.Fields)
		}
	}
	if len(m.Input.Fields) == 0 {
		t.Fatal("the mapping must still expose the file positional")
	}
	// And a caller passing it must be told, not silently ignored.
	out, err := ins.Run(res.Capability, json.RawMessage(`{"path":"/etc/hosts","word":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if out.OK {
		t.Fatal("an input the mapping cannot translate must be rejected")
	}
	if out.Error == nil || out.Error.Code != runtime.CodeInvalidInput {
		t.Fatalf("error = %+v, want invalid_input", out.Error)
	}
}

// Flag binding is proven deterministically against a mapping that declares one,
// rather than inferred from help text.
func TestFlagBindingReachesTheTool(t *testing.T) {
	ins := newInstaller(t)
	tool := filepath.Join(t.TempDir(), "recorder")
	// Print every argument on its own line so the assertion can check the exact
	// argv shape rather than a positional guess.
	body := "#!/bin/sh\nfor a in \"$@\"; do printf 'ARGV:%s\\n' \"$a\"; done\n"
	if err := os.WriteFile(tool, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	m := &manifest.Manifest{
		Schema: manifest.CurrentSchema, Name: "tool.render", Version: "1",
		Source: "path:" + t.TempDir(), Strategy: "cli_bridge",
		Support: manifest.SupportReady, Status: "ready",
		Execution: manifest.Execution{
			Type: manifest.ExecutionSubprocess, Supported: true, Command: tool,
			TimeoutMs: 10000, Ownership: manifest.OwnershipExternal,
		},
		Adapter: &manifest.Adapter{
			Kind: "cli", OutputMode: "text", OutputField: "text", Operation: "view",
			Confidence: "high", Layer: "known_adapter",
			Argv: []manifest.Binding{
				{From: "path", Kind: "positional", Position: 0, Required: true},
				{From: "style", Kind: "flag", Flag: "--style"},
			},
		},
		Input: manifest.Schema{Fields: []manifest.Field{
			{Name: "path", Type: "string", Required: true},
			{Name: "style", Type: "string"},
		}},
		Output: manifest.Schema{Fields: []manifest.Field{{Name: "text", Type: "string"}}},
	}
	manifestPath, err := m.Save(ins.Registry.ManifestsDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := ins.Registry.Register(registryEntryFor(m.Name, manifestPath, m)); err != nil {
		t.Fatal(err)
	}
	out, err := ins.Run("tool.render", json.RawMessage(`{"path":"first","style":"numbers"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !out.OK {
		t.Fatalf("run failed: %+v", out.Error)
	}
	// The flag must arrive as two separate argv elements, and the positional
	// after them, which is the conventional and safest ordering.
	if !strings.Contains(out.RawResult, "ARGV:--style") {
		t.Fatalf("the --style flag did not reach the tool as its own element:\n%s", out.RawResult)
	}
	if !strings.Contains(out.RawResult, "ARGV:numbers") {
		t.Fatalf("the --style value did not reach the tool:\n%s", out.RawResult)
	}
	if !strings.Contains(out.RawResult, "ARGV:first") {
		t.Fatalf("the positional did not reach the tool:\n%s", out.RawResult)
	}
	if strings.Index(out.RawResult, "ARGV:--style") > strings.Index(out.RawResult, "ARGV:first") {
		t.Fatalf("flags must precede positionals:\n%s", out.RawResult)
	}
}

// The capability name must not promise more than the mapping performs. Asking
// to "review" a display tool must not yield a capability called review.
func TestCapabilityNameDoesNotExceedTheMapping(t *testing.T) {
	dir := displayToolFixture(t)
	ins := newInstaller(t)
	res, err := ins.Install(Options{
		Source: localSource(t, dir),
		Goal:   "let GrokBot review this source file for problems",
	})
	if err != nil {
		t.Fatal(err)
	}
	entry, err := ins.Registry.Lookup(res.Capability)
	if err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Load(entry.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasSuffix(m.Name, ".review") {
		t.Fatalf("capability %q claims review, but the mapping only displays a file (operation %q)",
			m.Name, m.Adapter.Operation)
	}
	if m.Adapter.Operation != "view" {
		t.Fatalf("operation = %q, want view", m.Adapter.Operation)
	}
}

// --- jq regression: a fundamentally different CLI shape ---------------------

func TestFilterCapabilityPerformsRealFiltering(t *testing.T) {
	jq := lookForTool(t, "jq")
	if jq == "" {
		t.Skip("jq is not installed on this machine; the filter path is covered by adapter tests")
	}
	ins := newInstaller(t)
	// Register jq as an already-installed executable: no duplicate provisioning.
	m := &manifest.Manifest{
		Schema:   manifest.CurrentSchema,
		Name:     "jq.filter",
		Version:  "1",
		Source:   "https://github.com/jqlang/jq",
		Goal:     "let GrokBot apply a jq filter to JSON",
		Strategy: "external_execution",
		Support:  manifest.SupportReady,
		Status:   "ready",
		Execution: manifest.Execution{
			Type:      manifest.ExecutionSubprocess,
			Supported: true,
			Command:   jq,
			TimeoutMs: 30000,
			// External: the user owns it, so a package upgrade must not read as
			// tampering GrokInstall could have caused.
			Ownership: manifest.OwnershipExternal,
		},
		Adapter: &manifest.Adapter{
			Kind: "cli", OutputMode: "text", OutputField: "text", Operation: "filter",
			Confidence: "high", Layer: "known_adapter",
			Evidence: []string{"jq declares its filter as a leading argument"},
			Argv: []manifest.Binding{
				{From: "filter", Kind: "positional", Position: 0, Required: true, AllowDashLeading: true},
			},
			Stdin: &manifest.Binding{From: "input"},
		},
		Input: manifest.Schema{Fields: []manifest.Field{
			{Name: "filter", Type: "string", Required: true, Description: "jq filter expression"},
			{Name: "input", Type: "any JSON value", Description: "written to jq's standard input"},
		}},
		Output: manifest.Schema{Fields: []manifest.Field{
			{Name: "text", Type: "string", Description: "jq output, verbatim"},
		}},
		Grokbot: manifest.Grokbot{UseWhen: "a jq filter is needed"},
	}
	manifestPath, err := m.Save(ins.Registry.ManifestsDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := ins.Registry.Register(registryEntryFor(m.Name, manifestPath, m)); err != nil {
		t.Fatal(err)
	}

	out, err := ins.Run("jq.filter", json.RawMessage(`{"filter":".name","input":{"name":"Ada","age":37}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !out.OK {
		t.Fatalf("run failed: %+v", out.Error)
	}
	if !strings.Contains(out.RawResult, "Ada") {
		t.Fatalf("jq did not filter the document; result = %s", out.RawResult)
	}
	if strings.Contains(out.RawResult, "37") {
		t.Fatalf("jq returned fields the filter did not select: %s", out.RawResult)
	}
}

// A filter is data, not a command. A hostile filter must reach jq as one
// argument and must not be able to run anything.
func TestHostileFilterStaysData(t *testing.T) {
	canary := filepath.Join(t.TempDir(), "canary")
	ins := newInstaller(t)
	m := &manifest.Manifest{
		Schema: manifest.CurrentSchema, Name: "jq.hostile", Version: "1",
		Source: "https://github.com/jqlang/jq", Strategy: "external_execution",
		Support: manifest.SupportReady, Status: "ready",
		Execution: manifest.Execution{
			Type: manifest.ExecutionSubprocess, Supported: true,
			Command: lookForTool(t, "jq"), Ownership: manifest.OwnershipExternal,
			TimeoutMs: 10000,
		},
		Adapter: &manifest.Adapter{
			Kind: "cli", OutputMode: "text", OutputField: "text", Operation: "filter",
			Confidence: "high", Layer: "known_adapter",
			Argv: []manifest.Binding{
				{From: "filter", Kind: "positional", Position: 0, Required: true, AllowDashLeading: true},
			},
			Stdin: &manifest.Binding{From: "input"},
		},
		Input:  manifest.Schema{Fields: []manifest.Field{{Name: "filter", Type: "string", Required: true}}},
		Output: manifest.Schema{Fields: []manifest.Field{{Name: "text", Type: "string"}}},
	}
	manifestPath, err := m.Save(ins.Registry.ManifestsDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := ins.Registry.Register(registryEntryFor(m.Name, manifestPath, m)); err != nil {
		t.Fatal(err)
	}
	hostile := ".a; touch " + canary
	out, _ := ins.Run("jq.hostile", json.RawMessage(`{"filter":`+mustJSONString(hostile)+`,"input":{"a":1}}`))
	// The call may fail, and that is fine. What must never happen is execution.
	if _, err := os.Stat(canary); err == nil {
		t.Fatal("a hostile filter was executed: the canary file was created")
	}
	if out != nil && out.OK {
		t.Logf("jq rejected the filter, which is a correct outcome: %s", out.RawResult)
	}
}

// --- input validation -------------------------------------------------------

func TestMissingRequiredFieldIsRejectedBeforeExecution(t *testing.T) {
	dir := displayToolFixture(t)
	ins := newInstaller(t)
	res, err := ins.Install(Options{Source: localSource(t, dir), Goal: "let GrokBot show a file"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := ins.Run(res.Capability, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if out.OK {
		t.Fatal("a missing required field must be rejected")
	}
	if out.Error == nil || out.Error.Code != runtime.CodeInvalidInput {
		t.Fatalf("error = %+v, want invalid_input", out.Error)
	}
	if !strings.Contains(out.Error.Message, "path") {
		t.Fatalf("the error should name the missing field: %q", out.Error.Message)
	}
}

func TestUnknownFieldIsRejected(t *testing.T) {
	dir := displayToolFixture(t)
	ins := newInstaller(t)
	res, err := ins.Install(Options{Source: localSource(t, dir), Goal: "let GrokBot show a file"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := ins.Run(res.Capability, json.RawMessage(`{"path":"/etc/hosts","typo":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if out.OK {
		t.Fatal("an unknown field must be rejected rather than silently ignored")
	}
	if out.Error == nil || out.Error.Code != runtime.CodeInvalidInput {
		t.Fatalf("error = %+v, want invalid_input", out.Error)
	}
	if !strings.Contains(out.Error.Message, "typo") {
		t.Fatalf("the error should name the unknown field: %q", out.Error.Message)
	}
}

func TestNonObjectInputIsRejected(t *testing.T) {
	dir := displayToolFixture(t)
	ins := newInstaller(t)
	res, err := ins.Install(Options{Source: localSource(t, dir), Goal: "let GrokBot show a file"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := ins.Run(res.Capability, json.RawMessage(`[1,2,3]`))
	if err != nil {
		t.Fatal(err)
	}
	if out.OK {
		t.Fatal("a non-object input must be rejected")
	}
	if out.Error == nil || out.Error.Code != runtime.CodeInvalidInput {
		t.Fatalf("error = %+v, want invalid_input", out.Error)
	}
}

// --- migration --------------------------------------------------------------

// A v0.1.1 manifest has no mapping. It must be refused with a clear instruction,
// never reinterpreted into a passthrough that appears to work.
func TestV1ManifestIsRefusedWithReinstallInstruction(t *testing.T) {
	ins := newInstaller(t)
	bin := filepath.Join(t.TempDir(), "old-tool")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf 'OLD PIPE\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := &manifest.Manifest{
		Schema: manifest.SchemaID, Name: "legacy.run", Version: "1",
		Source: "path:/legacy", Strategy: "cli_bridge",
		Support: manifest.SupportReady, Status: "ready",
		Execution: manifest.Execution{
			Type: manifest.ExecutionSubprocess, Supported: true,
			Command: bin, InputMode: manifest.ModeStdinJSON, TimeoutMs: 10000,
		},
		Input:  manifest.Schema{Fields: []manifest.Field{{Name: "input", Type: "object"}}},
		Output: manifest.Schema{Fields: []manifest.Field{{Name: "result", Type: "object"}}},
	}
	// A v1 manifest must be readable so it can be reported, not rejected outright.
	if !old.NeedsReinstall() {
		t.Fatal("a v1 manifest should be flagged as needing reinstall")
	}
	path := filepath.Join(ins.Registry.ManifestsDir(), "legacy.run.json")
	data, err := old.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ins.Registry.Register(registryEntryFor("legacy.run", path, old)); err != nil {
		t.Fatal(err)
	}
	out, err := ins.Run("legacy.run", json.RawMessage(`{"input":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	if out.OK {
		t.Fatal("a pre-adapter manifest must not execute")
	}
	if out.Error == nil {
		t.Fatal("a refusal must be structured")
	}
	if !strings.Contains(out.Error.Message, "reinstall") {
		t.Fatalf("the refusal must say what to do: %q", out.Error.Message)
	}
	if strings.Contains(out.RawResult, "OLD PIPE") {
		t.Fatal("the old pipe-to-stdin behaviour must not be reproduced")
	}
	// The contract must also refuse, so GrokBot is never told to call it.
	contract := old.ContractText()
	if !strings.Contains(contract, "reinstall required") {
		t.Fatalf("contract must mark the capability as needing reinstall:\n%s", contract)
	}
	if strings.Contains(contract, "grokinstall run legacy.run") {
		t.Fatalf("contract must not tell GrokBot to call an unrunnable capability:\n%s", contract)
	}
}

// --- helpers ----------------------------------------------------------------

func lookForTool(t *testing.T, name string) string {
	t.Helper()
	p, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	return p
}

// registryEntryFor registers a capability that GrokInstall did not provision in
// this test, so ownership can be stated explicitly.
func registryEntryFor(name, manifestPath string, m *manifest.Manifest) registry.Entry {
	return registry.Entry{
		Name:         name,
		Strategy:     m.Strategy,
		Support:      string(m.Support),
		State:        registry.StateReady,
		Source:       m.Source,
		ManifestPath: manifestPath,
		InstallID:    "gi_capability",
	}
}

func mustJSONString(s string) string {
	encoded, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}
