// Package diagnostics answers "what is wrong with this installation?" with
// prioritized, actionable findings. It never guesses: every check reports the
// evidence it used.
package diagnostics

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"grokinstall/internal/registry"
	"grokinstall/internal/toolchain"
)

// Status is the health of one check.
type Status string

// Check statuses.
const (
	StatusOK   Status = "ok"
	StatusWarn Status = "warn"
	StatusFail Status = "fail"
)

// severity orders statuses for reporting.
var severity = map[Status]int{StatusFail: 0, StatusWarn: 1, StatusOK: 2}

// Check is one diagnostic result.
type Check struct {
	Name     string `json:"name"`
	Status   Status `json:"status"`
	Required bool   `json:"required"`
	Detail   string `json:"detail"`
	Action   string `json:"action,omitempty"`
}

// Report is the full doctor result.
type Report struct {
	StateDir string  `json:"state_dir"`
	Healthy  bool    `json:"healthy"`
	Checks   []Check `json:"checks"`
	Summary  Summary `json:"summary"`
}

// Summary counts check outcomes.
type Summary struct {
	OK   int `json:"ok"`
	Warn int `json:"warn"`
	Fail int `json:"fail"`
}

// Check returns a check by name.
func (r *Report) Check(name string) *Check {
	for i := range r.Checks {
		if r.Checks[i].Name == name {
			return &r.Checks[i]
		}
	}
	return nil
}

// Problems returns failures and warnings, most severe first.
func (r *Report) Problems() []Check {
	var out []Check
	for _, c := range r.Checks {
		if c.Status != StatusOK {
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return severity[out[i].Status] < severity[out[j].Status] })
	return out
}

// Run performs every diagnostic check against a state directory and toolchain.
func Run(stateDir string, profile *toolchain.Profile) *Report {
	rep := &Report{StateDir: stateDir, Healthy: true}
	add := func(c Check) {
		rep.Checks = append(rep.Checks, c)
		switch c.Status {
		case StatusOK:
			rep.Summary.OK++
		case StatusWarn:
			rep.Summary.Warn++
		case StatusFail:
			rep.Summary.Fail++
			rep.Healthy = false
		}
	}

	add(checkStateDir(stateDir))
	reg, regErr := registry.New(stateDir)
	if regErr == nil {
		add(checkConfig(reg))
		add(checkRegistry(reg))
		add(checkDirs(reg))
	} else {
		add(Check{Name: "state_dir", Status: StatusFail, Required: true,
			Detail: regErr.Error(), Action: "choose a writable state directory (set GROKINSTALL_HOME)"})
	}
	for _, id := range profile.Order() {
		add(checkTool(profile, id))
	}

	// The ability to inspect GitHub sources is a hard requirement only when
	// the user actually asks for a remote source.
	add(checkGitHubReadiness(profile))
	return rep
}

func checkStateDir(dir string) Check {
	if dir == "" {
		return Check{Name: "state_dir", Status: StatusFail, Required: true,
			Detail: "no state directory configured", Action: "set GROKINSTALL_HOME to a writable path"}
	}
	info, err := os.Stat(dir)
	if err != nil {
		return Check{Name: "state_dir", Status: StatusFail, Required: true,
			Detail: fmt.Sprintf("cannot read %s: %v", dir, err), Action: "create the directory or fix permissions"}
	}
	if !info.IsDir() {
		return Check{Name: "state_dir", Status: StatusFail, Required: true,
			Detail: fmt.Sprintf("%s is not a directory", dir), Action: "point GROKINSTALL_HOME at a directory"}
	}
	probe := filepath.Join(dir, ".doctor-probe")
	if err := os.WriteFile(probe, []byte("ok"), 0o644); err != nil {
		return Check{Name: "state_dir", Status: StatusFail, Required: true,
			Detail: fmt.Sprintf("%s is not writable: %v", dir, err),
			Action: "fix directory permissions, or set GROKINSTALL_HOME somewhere writable"}
	}
	_ = os.Remove(probe)
	return Check{Name: "state_dir", Status: StatusOK, Required: true, Detail: dir + " is writable"}
}

func checkConfig(reg *registry.Registry) Check {
	if _, err := reg.LoadConfig(); err != nil {
		return Check{Name: "config", Status: StatusFail, Required: true,
			Detail: err.Error(), Action: "fix or delete " + reg.ConfigPath() + " (a default will be recreated)"}
	}
	return Check{Name: "config", Status: StatusOK, Required: true, Detail: reg.ConfigPath() + " is valid"}
}

func checkRegistry(reg *registry.Registry) Check {
	if _, err := reg.List(); err != nil {
		return Check{Name: "registry", Status: StatusFail, Required: true,
			Detail: err.Error(), Action: "repair or delete " + reg.RegistryPath() + " (it is rebuilt on install)"}
	}
	return Check{Name: "registry", Status: StatusOK, Required: true, Detail: reg.RegistryPath() + " is valid"}
}

func checkDirs(reg *registry.Registry) Check {
	for _, d := range []struct{ name, path string }{
		{"manifests", reg.ManifestsDir()},
		{"adapters", reg.AdaptersDir()},
		{"receipts", reg.ReceiptsDir()},
		{"diagnostics", reg.DiagnosticsDir()},
		{"logs", reg.LogsDir()},
	} {
		if info, err := os.Stat(d.path); err != nil || !info.IsDir() {
			return Check{Name: "state_dirs", Status: StatusFail, Required: true,
				Detail: fmt.Sprintf("%s directory missing", d.name),
				Action: "recreate the state directory (any command will rebuild it)"}
		}
	}
	return Check{Name: "state_dirs", Status: StatusOK, Required: true, Detail: "all state directories are present"}
}

// optionalTools never make GrokInstall unhealthy: they are capabilities, not
// prerequisites.
var optionalTools = map[toolchain.ToolID]bool{
	toolchain.ToolGh:       true,
	toolchain.ToolPnpm:     true,
	toolchain.ToolUv:       true,
	toolchain.ToolDocker:   true,
	toolchain.ToolOpenCode: true,
	toolchain.ToolCursor:   true,
	toolchain.ToolVercel:   true,
	toolchain.ToolOllama:   true,
}

func checkTool(profile *toolchain.Profile, id toolchain.ToolID) Check {
	tool := profile.Get(id)
	name := "tool:" + string(id)
	if tool.Present {
		detail := tool.Name + " detected"
		if tool.Version != "" {
			detail = tool.Name + " " + firstLine(tool.Version)
		}
		return Check{Name: name, Status: StatusOK, Required: !optionalTools[id], Detail: detail}
	}
	if optionalTools[id] {
		return Check{Name: name, Status: StatusWarn, Required: false,
			Detail: tool.Name + " not detected",
			Action: "optional: " + tool.Why}
	}
	action := "install " + tool.Name + " to use sources that need it"
	if sub, ok := profile.SubstituteFor(id); ok {
		action = fmt.Sprintf("install %s, or rely on the %s substitute that is already present", tool.Name, sub)
	}
	return Check{Name: name, Status: StatusWarn, Required: false, Detail: tool.Name + " not detected", Action: action}
}

func checkGitHubReadiness(profile *toolchain.Profile) Check {
	if profile.Get(toolchain.ToolGit).Present {
		return Check{Name: "github_inspection", Status: StatusOK, Required: false,
			Detail: "public GitHub repositories can be inspected with git"}
	}
	return Check{Name: "github_inspection", Status: StatusWarn, Required: false,
		Detail: "public GitHub inspection needs git",
		Action: "install Git to inspect remote repositories; local directories still work without it"}
}

func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	if len(s) > 60 {
		return s[:60]
	}
	return s
}
