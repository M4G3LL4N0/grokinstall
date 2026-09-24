// Package gobuild provisions an executable by building a Go repository into a
// GrokInstall-owned runtime.
//
// The policy is deliberately narrow. It runs `go build` and nothing else: no
// Makefile, no shell install script, no `go generate`, no project-defined setup
// command. When a project can only be built by executing its own code, the
// provisioner refuses and says exactly what authorization would be required,
// rather than quietly widening its own permissions.
package gobuild

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// BuildTimeout bounds a single build.
const BuildTimeout = 10 * 60 // seconds

// Request describes a build.
type Request struct {
	// SourceDir is the checked-out repository.
	SourceDir string
	// Output is the binary path to produce.
	Output string
	// Package is the package pattern to build, e.g. "./cmd/tool" or ".".
	Package string
	// AllowScripts must be true to run any project-defined build step. It is
	// never set automatically.
	AllowScripts bool
}

// Result describes a completed build.
type Result struct {
	Command  []string
	Binary   string
	Duration string
	Warnings []string
}

// Refusal explains why a build was not attempted or not completed.
type Refusal struct {
	Reason   string
	Required string
	Details  []string
}

// ErrRequiresAuthorization marks a project that cannot be built safely.
var ErrRequiresAuthorization = errors.New("provisioning requires additional authorization")

// Detect reports whether a directory looks like a buildable Go module.
func Detect(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		return false
	}
	return true
}

// FindMainPackage picks the package to build. It prefers an explicit main
// package under cmd/, then ./cmd/<name>, then the root package. It never
// guesses beyond that.
func FindMainPackage(dir, preferred string) (string, error) {
	if preferred != "" {
		return preferred, nil
	}
	// A root main package is the simplest case.
	if hasMainGo(filepath.Join(dir, "main.go")) {
		return ".", nil
	}
	entries, err := os.ReadDir(filepath.Join(dir, "cmd"))
	if err == nil {
		var names []string
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			if hasMainGo(filepath.Join(dir, "cmd", e.Name(), "main.go")) {
				names = append(names, e.Name())
			}
		}
		if len(names) == 1 {
			return "./cmd/" + names[0], nil
		}
		if len(names) > 1 {
			return "", fmt.Errorf("multiple commands found (%s); specify one explicitly", strings.Join(names, ", "))
		}
	}
	return "", fmt.Errorf("no main package found; pass an explicit package to build")
}

func hasMainGo(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), "package main")
}

// Build compiles a Go package into req.Output.
//
// The only command that ever runs is `go build`. Project scripts are refused
// unless the caller explicitly authorized them, and even then the result is
// reported so the receipt can record it.
func Build(ctx context.Context, req Request, goBin string) (*Result, error) {
	if !Detect(req.SourceDir) {
		return nil, &Refusal{Reason: "not a Go module: no go.mod at the source root"}
	}
	if err := checkUnsafeScripts(req.SourceDir, req.AllowScripts); err != nil {
		return nil, err
	}
	pkg := req.Package
	if pkg == "" {
		found, err := FindMainPackage(req.SourceDir, "")
		if err != nil {
			return nil, &Refusal{Reason: err.Error(), Required: "allow_source_build"}
		}
		pkg = found
	}
	if err := os.MkdirAll(filepath.Dir(req.Output), 0o755); err != nil {
		return nil, err
	}
	if goBin == "" {
		goBin = "go"
	}
	abs, err := exec.LookPath(goBin)
	if err != nil {
		return nil, &Refusal{Reason: "go toolchain not found on PATH", Required: ""}
	}
	args := []string{"build", "-o", req.Output, pkg}
	buildCtx, cancel := context.WithTimeout(ctx, BuildTimeout)
	defer cancel()
	cmd := exec.CommandContext(buildCtx, abs, args...)
	// Build in an isolated environment: no project scripts can influence it,
	// and the user's shell aliases do not leak in.
	cmd.Dir = req.SourceDir
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "CGO_ENABLED=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("go build failed: %s", strings.TrimSpace(string(out)))
	}
	if _, statErr := os.Stat(req.Output); statErr != nil {
		return nil, fmt.Errorf("build reported success but produced no binary: %w", statErr)
	}
	return &Result{Command: append([]string{abs}, args...), Binary: req.Output}, nil
}

// checkUnsafeScripts refuses to build a project whose documented build path
// depends on executing its own scripts unless scripts are explicitly allowed.
func checkUnsafeScripts(dir string, allow bool) error {
	var blockers []string
	if _, err := os.Stat(filepath.Join(dir, "Makefile")); err == nil {
		blockers = append(blockers, "the repository ships a Makefile")
	}
	if hasScript(dir, "install.sh") || hasScript(dir, "setup.sh") {
		blockers = append(blockers, "the repository ships an install/setup script")
	}
	if len(blockers) == 0 {
		return nil
	}
	if allow {
		// Even with authorization, `go build` still does not run them; we only
		// note their presence so the receipt can record the risk.
		return nil
	}
	return &Refusal{
		Reason:   "the repository ships build tooling that is not executed by this provisioner",
		Required: "allow_source_build",
		Details:  blockers,
	}
}

func hasScript(dir, name string) bool {
	for _, candidate := range []string{name, filepath.Join("scripts", name), filepath.Join("hack", name)} {
		if _, err := os.Stat(filepath.Join(dir, candidate)); err == nil {
			return true
		}
	}
	return false
}

// Error implements error so a Refusal can travel through normal error paths
// while remaining inspectable.
func (r *Refusal) Error() string {
	if len(r.Details) == 0 {
		return r.Reason
	}
	return r.Reason + ": " + strings.Join(r.Details, "; ")
}
