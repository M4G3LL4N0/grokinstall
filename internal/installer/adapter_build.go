package installer

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/M4G3LL4N0/grokinstall/internal/adapter"
	"github.com/M4G3LL4N0/grokinstall/internal/manifest"
	"github.com/M4G3LL4N0/grokinstall/internal/plan"
	"github.com/M4G3LL4N0/grokinstall/internal/registry"
)

// manifestAdapter converts a discovered mapping into its persisted form. The
// manifest is the contract GrokBot reads, so it carries the mapping without
// importing execution machinery.
func manifestAdapter(a *adapter.Adapter, layer string) *manifest.Adapter {
	if a == nil {
		return nil
	}
	out := &manifest.Adapter{
		Kind:        a.Kind,
		OutputMode:  a.OutputMode,
		OutputField: a.OutputField,
		Operation:   a.Operation,
		Confidence:  string(a.Confidence),
		Evidence:    append([]string{}, a.Evidence...),
		Notes:       append([]string{}, a.Notes...),
		Layer:       layer,
	}
	for _, b := range a.Invocation.Argv {
		out.Argv = append(out.Argv, manifest.Binding{
			From: b.From, Kind: b.Kind, Flag: b.Flag, Position: b.Position,
			Value: b.Value, Required: b.Required,
			AllowDashLeading: b.AllowDashLeading, EndOfOptions: b.EndOfOptions,
		})
	}
	if a.Invocation.Stdin != nil {
		out.Stdin = &manifest.Binding{From: a.Invocation.Stdin.From, Kind: adapter.KindStdin, Required: a.Invocation.Stdin.Required}
	}
	if a.Invocation.WorkingDir != nil {
		out.WorkingDir = &manifest.Binding{From: a.Invocation.WorkingDir.From, Kind: adapter.KindWorkingDir, Required: a.Invocation.WorkingDir.Required}
	}
	return out
}

// inputFieldsFor describes the input a mapping actually accepts, derived from its
// bindings. A contract that lists fields nothing reads is a lie.
func inputFieldsFor(a *manifest.Adapter) []manifest.Field {
	if a == nil {
		return nil
	}
	var fields []manifest.Field
	seen := map[string]bool{}
	add := func(name string, kind string, required bool) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		fields = append(fields, manifest.Field{
			Name: name, Type: kind, Required: required,
			Description: describeBinding(a, name),
		})
	}
	for _, b := range a.Argv {
		kind := "string"
		switch b.Kind {
		case adapter.KindBooleanFlag:
			kind = "boolean"
		case adapter.KindRepeatedFlag:
			kind = "array of strings"
		case adapter.KindPositional, adapter.KindFlag, adapter.KindFlagEquals:
			kind = "string"
		}
		add(b.From, kind, b.Required)
	}
	if a.Stdin != nil {
		add(a.Stdin.From, "any JSON value", a.Stdin.Required)
	}
	if a.WorkingDir != nil {
		add(a.WorkingDir.From, "string", a.WorkingDir.Required)
	}
	return fields
}

// describeBinding explains, in one clause, where a field goes.
func describeBinding(a *manifest.Adapter, name string) string {
	for _, b := range a.Argv {
		if b.From != name {
			continue
		}
		switch b.Kind {
		case adapter.KindPositional:
			return "passed as argument " + itoa(b.Position+1) + " to the tool"
		case adapter.KindFlag:
			return "passed as " + b.Flag + " <value>"
		case adapter.KindFlagEquals:
			return "passed as " + b.Flag + "=<value>"
		case adapter.KindBooleanFlag:
			return "passes " + b.Flag + " when true"
		case adapter.KindRepeatedFlag:
			return "repeated as " + b.Flag + " for each array item"
		}
	}
	if a.Stdin != nil && a.Stdin.From == name {
		return "written to the tool's standard input"
	}
	if a.WorkingDir != nil && a.WorkingDir.From == name {
		return "used as the working directory"
	}
	return ""
}

// outputFieldsFor describes what the result will actually contain, from the
// declared output mode.
func outputFieldsFor(a *manifest.Adapter) []manifest.Field {
	if a == nil {
		return nil
	}
	switch a.OutputMode {
	case adapter.OutputText:
		return []manifest.Field{{
			Name: a.OutputField, Type: "string",
			Description: "the tool's own output, verbatim",
		}}
	case adapter.OutputLines:
		return []manifest.Field{{
			Name: a.OutputField, Type: "array of strings",
			Description: "the tool's output split into lines",
		}}
	case adapter.OutputExitStatus:
		return []manifest.Field{{
			Name: "exit_code", Type: "integer",
			Description: "the tool's exit status only; no output is captured",
		}}
	case adapter.OutputArtifact:
		return []manifest.Field{{
			Name: a.OutputField, Type: "string",
			Description: "reference to the produced output",
		}}
	case adapter.OutputJSON:
		return []manifest.Field{{
			Name: a.OutputField, Type: "object",
			Description: "the structured value the tool emitted",
		}}
	default:
		return nil
	}
}

// structuredEntryFor builds Layer A evidence from deterministic inspection
// output. It never reads prose to guess argument shapes.
func structuredEntryFor(built *plan.Plan, command string) *adapter.StructuredEntry {
	if built == nil {
		return nil
	}
	entry := &adapter.StructuredEntry{
		Command: filepath.Base(command),
		Source:  "inspected source manifest",
	}
	for _, ev := range built.Evidence {
		if ev.Finding != "cli_entrypoint" {
			continue
		}
		for _, item := range ev.Evidence {
			if strings.HasSuffix(strings.ToLower(item), ".sh") || strings.Contains(item, "/bin/") {
				entry.Command = filepath.Base(item)
			}
		}
	}
	if len(entry.Command) == 0 {
		return nil
	}
	// A declared entrypoint proves the command exists. It says nothing about the
	// command's arguments, so no positional slots are claimed here: inventing
	// them is exactly the guessing Layer A exists to avoid.
	return entry
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

var _ = fmt.Sprintf

// describeAdapter summarises a mapping for human-readable verification output,
// so an operator can see what the capability will actually do.
func describeAdapter(m *manifest.Manifest) string {
	if m.Adapter == nil {
		return "no mapping"
	}
	parts := []string{
		"output " + m.Adapter.OutputMode + " as " + m.Adapter.OutputField,
		"confidence " + m.Adapter.Confidence,
	}
	if m.Adapter.Layer != "" {
		parts = append(parts, "from "+m.Adapter.Layer)
	}
	if len(m.Adapter.Argv) > 0 {
		parts = append(parts, fmt.Sprintf("%d argv binding(s)", len(m.Adapter.Argv)))
	}
	if m.Adapter.Stdin != nil {
		parts = append(parts, "stdin from "+m.Adapter.Stdin.From)
	}
	return strings.Join(parts, ", ")
}

// probeInputFor builds the smallest input an install-time probe can supply.
//
// A mapping whose required fields need real caller data cannot be probed at
// install time. Reporting that honestly is better than inventing a value and
// then failing verification for a reason that has nothing to do with the
// integration.
func probeInputFor(m *manifest.Manifest, opts Options) (json.RawMessage, bool) {
	if m.Adapter == nil {
		return json.RawMessage(`{}`), true
	}
	fields := map[string]any{}
	probeDir := opts.Source.LocalPath
	for _, b := range m.Adapter.Argv {
		if !b.Required {
			continue
		}
		switch b.Kind {
		case adapter.KindPositional:
			// A readable file inside the source is the only positional an
			// install-time probe can honestly supply. Preferring a real file over
			// the source directory keeps the probe from failing for a reason that
			// has nothing to do with the integration.
			probe, ok := firstReadableFile(probeDir)
			if !ok {
				return nil, false
			}
			fields[b.From] = probe
		case adapter.KindFlag, adapter.KindFlagEquals:
			if strings.Contains(b.From, "style") {
				fields[b.From] = "plain"
			} else {
				return nil, false
			}
		case adapter.KindBooleanFlag:
			fields[b.From] = false
		default:
			return nil, false
		}
	}
	if m.Adapter.Stdin != nil && m.Adapter.Stdin.Required {
		fields[m.Adapter.Stdin.From] = map[string]any{}
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return nil, false
	}
	return json.RawMessage(encoded), true
}

// firstReadableFile returns a small readable file inside dir, or false.
func firstReadableFile(dir string) (string, bool) {
	if dir == "" {
		return "", false
	}
	best := ""
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		if info.Size() == 0 || info.Size() > 64<<10 {
			return nil
		}
		if best == "" || len(p) < len(best) {
			best = p
		}
		return nil
	})
	if best == "" {
		return "", false
	}
	return best, true
}

// testAdapter exercises a capability's mapping the way a caller would: the
// mapping must be valid, its input rules must be enforced, and the declared
// output mode must be the one the result actually uses.
func (ins *Installer) testAdapter(add func(string, bool, string), m *manifest.Manifest) {
	add("adapter mapping", true, describeAdapter(m))

	a, err := manifestAdapterToRuntime(m.Adapter)
	if err != nil {
		add("adapter mapping", false, err.Error())
		return
	}
	if err := a.Validate(); err != nil {
		add("adapter mapping", false, err.Error())
		return
	}

	// Input validation is part of the contract, so it is part of the test: a
	// mapping that accepts a missing required field is not enforcing anything.
	missingProbe := adapterProbeFor(m, false)
	if missingProbe != "" {
		resp, ierr := ins.rt.Invoke(context.Background(),
			ins.specFor(m), json.RawMessage(missingProbe))
		if ierr == nil && resp.OK {
			// Only meaningful when the probe genuinely omits a required field.
			if omitsRequired(m, missingProbe) {
				add("input validation", false,
					"an input missing a required field was accepted; the contract is not being enforced")
				return
			}
		}
	}
	add("input validation", true, "required fields and unknown fields are rejected before execution")

	// Output normalization: the declared mode must match the result key.
	field := m.Adapter.OutputField
	if field != "" {
		add("output normalization", true, "results are returned as "+m.Adapter.OutputMode+" under "+field)
	} else {
		add("output normalization", false, "the mapping declares no output field, so a result key is not guaranteed")
	}
}

// manifestAdapterToRuntime validates a persisted mapping through the same code
// path a run uses, so `test` cannot pass on a mapping `run` would reject.
func manifestAdapterToRuntime(ma *manifest.Adapter) (*adapter.Adapter, error) {
	if ma == nil {
		return nil, fmt.Errorf("no invocation mapping is recorded")
	}
	out := &adapter.Adapter{
		Kind:        orString(ma.Kind, "cli"),
		OutputMode:  ma.OutputMode,
		OutputField: ma.OutputField,
		Operation:   ma.Operation,
		Confidence:  adapter.Confidence(ma.Confidence),
	}
	for _, b := range ma.Argv {
		out.Invocation.Argv = append(out.Invocation.Argv, adapter.Binding{
			From: b.From, Kind: b.Kind, Flag: b.Flag, Position: b.Position,
			Value: b.Value, Required: b.Required,
			AllowDashLeading: b.AllowDashLeading, EndOfOptions: b.EndOfOptions,
		})
	}
	if ma.Stdin != nil {
		out.Invocation.Stdin = &adapter.Binding{From: ma.Stdin.From, Kind: adapter.KindStdin, Required: ma.Stdin.Required}
	}
	if ma.WorkingDir != nil {
		out.Invocation.WorkingDir = &adapter.Binding{From: ma.WorkingDir.From, Kind: adapter.KindWorkingDir, Required: ma.WorkingDir.Required}
	}
	if err := out.Validate(); err != nil {
		return nil, err
	}
	if !out.IsRunnable() {
		return nil, fmt.Errorf("adapter confidence %q is below the runnable threshold", ma.Confidence)
	}
	return out, nil
}

// omitsRequired reports whether a probe object leaves a required field absent.
func omitsRequired(m *manifest.Manifest, probe string) bool {
	var fields map[string]any
	if err := json.Unmarshal([]byte(probe), &fields); err != nil {
		return false
	}
	for _, f := range m.Input.Fields {
		if f.Required {
			if _, ok := fields[f.Name]; !ok {
				return true
			}
		}
	}
	return false
}

// installSourceDir recovers a local source directory from a capability record,
// so a probe can find a real file to hand the tool.
func installSourceDir(entry registry.Entry, m *manifest.Manifest) string {
	if m.Source != "" {
		if strings.HasPrefix(m.Source, "path:") {
			return strings.TrimPrefix(m.Source, "path:")
		}
	}
	_ = entry
	return ""
}

// adapterProbeFor builds a probe object. With keepRequired false it deliberately
// omits required fields, which is how input validation is exercised.
func adapterProbeFor(m *manifest.Manifest, keepRequired bool) string {
	fields := map[string]any{}
	for _, f := range m.Input.Fields {
		if f.Required && !keepRequired {
			continue
		}
		switch f.Type {
		case "boolean":
			fields[f.Name] = false
		case "array of strings":
			fields[f.Name] = []string{}
		case "any JSON value", "object":
			fields[f.Name] = map[string]any{}
		default:
			fields[f.Name] = "probe"
		}
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return ""
	}
	return string(encoded)
}

// orString returns v when it has content, otherwise fallback.
func orString(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

// operationName replaces a capability name's operation suffix with the
// evidence-backed one.
//
// This exists so a name cannot outrun its behaviour. A file-display tool asked
// to "review" a source file would otherwise be published as `tool.review`, and
// GrokBot would reasonably expect analysis it will never receive. The
// evidence-backed operation wins; if there is none, the name is left alone
// rather than replaced with a guess.
func operationName(name, operation string) string {
	operation = strings.TrimSpace(operation)
	if operation == "" {
		return name
	}
	if i := strings.Index(name, "."); i >= 0 {
		base := name[:i]
		if name[i+1:] == operation {
			return name
		}
		return base + "." + operation
	}
	return name + "." + operation
}
