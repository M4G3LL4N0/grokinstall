// Package toolchain detects which support tools exist on this machine and how
// a strategy's needs can be satisfied with what is installed. It only reports
// what is present; it never recommends installing everything.
package toolchain

import (
	"context"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// Status describes availability relative to a need.
type Status string

const (
	StatusPresent    Status = "present"
	StatusMissing    Status = "missing"
	StatusSubstitute Status = "substitute_available"
)

// ToolID identifies a tool in the catalogue.
type ToolID string

// The tool catalogue.
const (
	ToolGit      ToolID = "git"
	ToolGh       ToolID = "gh"
	ToolGo       ToolID = "go"
	ToolNode     ToolID = "node"
	ToolPnpm     ToolID = "pnpm"
	ToolNpm      ToolID = "npm"
	ToolPython   ToolID = "python"
	ToolUv       ToolID = "uv"
	ToolDocker   ToolID = "docker"
	ToolOpenCode ToolID = "opencode"
	ToolCursor   ToolID = "cursor"
	ToolVercel   ToolID = "vercel"
	ToolOllama   ToolID = "ollama"
)

// Role groups tools by purpose.
type Role string

const (
	RoleVCS        Role = "vcs"
	RoleRuntime    Role = "runtime"
	RolePkgManager Role = "package_manager"
	RoleContainer  Role = "container"
	RoleCoder      Role = "coding_agent"
	RoleProvider   Role = "provider"
	RoleCLI        Role = "cli"
)

// Tool is the detected state of a single tool.
type Tool struct {
	ID          ToolID   `json:"id"`
	Name        string   `json:"name"`
	Role        Role     `json:"role"`
	Present     bool     `json:"present"`
	Version     string   `json:"version,omitempty"`
	Substitutes []ToolID `json:"substitutes,omitempty"`
	Why         string   `json:"why"`
}

// Resolution pairs a need with how it can be met on this machine.
type Resolution struct {
	Need     ToolID `json:"need"`
	Resolved ToolID `json:"resolved"`
	Present  bool   `json:"present"`
	Status   Status `json:"status"`
}

// Profile is the full detection result for this machine.
type Profile struct {
	Tools map[ToolID]Tool `json:"tools"`
}

type catalogueEntry struct {
	id          ToolID
	name        string
	role        Role
	versionArgs []string
	substitutes []ToolID
	why         string
}

var catalogue = []catalogueEntry{
	{ToolGit, "Git", RoleVCS, []string{"--version"}, nil, "version control; fetching public repositories and resolving commit identities"},
	{ToolGh, "GitHub CLI", RoleCLI, []string{"--version"}, []ToolID{ToolGit}, "optional GitHub operations; git can substitute for public repository fetch"},
	{ToolGo, "Go", RoleRuntime, []string{"version"}, nil, "Go toolchain; required to build Go sources"},
	{ToolNode, "Node.js", RoleRuntime, []string{"--version"}, nil, "runtime for JavaScript/TypeScript tooling"},
	{ToolPnpm, "pnpm", RolePkgManager, []string{"--version"}, []ToolID{ToolNpm}, "fast JavaScript package manager"},
	{ToolNpm, "npm", RolePkgManager, []string{"--version"}, []ToolID{ToolPnpm}, "JavaScript package manager that ships with Node"},
	{ToolPython, "Python", RoleRuntime, []string{"--version"}, nil, "runtime for Python sources"},
	{ToolUv, "uv", RolePkgManager, []string{"--version"}, []ToolID{ToolPython}, "fast Python package/environment manager"},
	{ToolDocker, "Docker", RoleContainer, []string{"--version"}, nil, "containerization; running Docker-based capabilities"},
	{ToolOpenCode, "OpenCode", RoleCoder, []string{"--version"}, []ToolID{ToolCursor}, "reasoning/coding agent for generated adapter work"},
	{ToolCursor, "Cursor", RoleCoder, []string{"--version"}, []ToolID{ToolOpenCode}, "reasoning/coding agent for generated adapter work"},
	{ToolVercel, "Vercel", RoleProvider, []string{"--version"}, nil, "optional deployment provider"},
	{ToolOllama, "Ollama", RoleProvider, []string{"--version"}, nil, "optional local model server"},
}

// Detect assembles a Profile for this machine.
func Detect() *Profile {
	p := &Profile{Tools: map[ToolID]Tool{}}
	results := make(chan struct {
		id ToolID
		ok bool
		v  string
	}, len(catalogue))
	for _, c := range catalogue {
		c := c
		go func() {
			ok, v := probe(c.id, c.versionArgs)
			results <- struct {
				id ToolID
				ok bool
				v  string
			}{c.id, ok, v}
		}()
	}
	for range catalogue {
		r := <-results
		c := toolFor(r.id)
		t := Tool{
			ID:          c.id,
			Name:        c.name,
			Role:        c.role,
			Substitutes: c.substitutes,
			Why:         c.why,
			Present:     r.ok,
			Version:     r.v,
		}
		p.Tools[t.ID] = t
	}
	return p
}

func toolFor(id ToolID) catalogueEntry {
	for _, c := range catalogue {
		if c.id == id {
			return c
		}
	}
	return catalogueEntry{id: id, name: string(id)}
}

func probe(id ToolID, args []string) (bool, string) {
	if id == ToolPython {
		for _, cand := range []string{"python3", "python"} {
			if ok, v := probeBinary(cand, args); ok {
				return true, v
			}
		}
		return false, ""
	}
	return probeBinary(string(id), args)
}

func probeBinary(name string, args []string) (bool, string) {
	bin, err := exec.LookPath(name)
	if err != nil {
		return false, ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	out, err := cmd.Output()
	if err != nil {
		return true, ""
	}
	return true, strings.TrimSpace(string(out))
}

// Get returns the detection row for a tool (zero value if unknown).
func (p *Profile) Get(id ToolID) Tool {
	t, ok := p.Tools[id]
	if !ok {
		return Tool{ID: id, Present: false}
	}
	return t
}

// Resolve determines how a tool need can be met: by itself when present, by a
// declared substitute when that substitute is present, otherwise missing.
func (p *Profile) Resolve(id ToolID) (Resolution, bool) {
	t := p.Get(id)
	if t.Present {
		return Resolution{Need: id, Resolved: id, Present: true, Status: StatusPresent}, true
	}
	for _, sub := range t.Substitutes {
		st := p.Get(sub)
		if st.Present {
			return Resolution{Need: id, Resolved: sub, Present: true, Status: StatusSubstitute}, true
		}
	}
	return Resolution{Need: id, Resolved: "", Present: false, Status: StatusMissing}, false
}

// Order returns tool ids in stable catalogue order.
func (p *Profile) Order() []ToolID {
	ids := make([]ToolID, 0, len(p.Tools))
	for _, c := range catalogue {
		ids = append(ids, c.id)
	}
	sort.SliceStable(ids, func(i, j int) bool { return string(ids[i]) < string(ids[j]) })
	return ids
}

// IsCodingTool reports whether a tool acts as a reasoning/coding agent.
func (p *Profile) IsCodingTool(id ToolID) bool {
	return p.Get(id).Role == RoleCoder
}

// HomeDir returns the user home directory.
func HomeDir() (string, error) {
	if h := os.Getenv("GROKINSTALL_HOME"); h != "" {
		return h, nil
	}
	return os.UserHomeDir()
}
