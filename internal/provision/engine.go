package provision

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"grokinstall/internal/provision/github"
	"grokinstall/internal/provision/gobuild"
	"grokinstall/internal/registry"
)

// Engine discovers and performs provisioning for a required executable.
type Engine struct {
	// RuntimesDir is the GrokInstall-owned runtime root.
	RuntimesDir string
	// Policy governs what may run.
	Policy Policy
	// GitHub reads release metadata. A test server can replace it.
	GitHub *github.Client
	// AllowSourceBuild permits compiling untrusted source.
	AllowSourceBuild bool
	// WorkDir is where downloads and checkouts happen before commit.
	WorkDir string
}

// Need describes the executable a capability requires.
type Need struct {
	// Name is the command name being looked for.
	Name string
	// Owner and Repo identify the upstream project for release lookup.
	Owner string
	Repo  string
	// PreferExisting is a path to a binary the user already has.
	PreferExisting string
	// SourceDir is a local checkout that could be built.
	SourceDir string
	// Version pins a release tag.
	Version string
	// Ecosystem names the packaging ecosystem the source declares, so an honest
	// alternative can be offered even when no local checkout is available.
	Ecosystem string
}

// Discover returns every provisioning route it can find, in policy order.
func (e *Engine) Discover(ctx context.Context, need Need) []Candidate {
	var out []Candidate

	if need.PreferExisting != "" {
		if info, err := os.Stat(need.PreferExisting); err == nil && !info.IsDir() {
			out = append(out, Candidate{
				Method: MethodExisting, Path: need.PreferExisting,
				Ownership: OwnershipExternal, Reversible: true,
				Verification: "execute the command and check it starts",
				Notes:        []string{"uses software you already installed; GrokInstall changes nothing"},
			})
		}
	}
	if path, err := exec.LookPath(need.Name); err == nil {
		out = append(out, Candidate{
			Method: MethodExisting, Path: path,
			Ownership: OwnershipExternal, Reversible: true,
			Verification: "execute the command and check it starts",
			Notes:        []string{"already on PATH; nothing needs installing"},
		})
	}

	if need.Owner != "" && need.Repo != "" && e.GitHub != nil {
		if cand, ok := e.releaseCandidate(ctx, need); ok {
			out = append(out, cand)
		}
	}

	if need.SourceDir != "" && gobuild.Detect(need.SourceDir) {
		risk := []string{"compiles untrusted source on this machine"}
		requires := []Authorization(nil)
		if !e.AllowSourceBuild {
			requires = []Authorization{AuthSourceBuild}
		}
		out = append(out, Candidate{
			Method: MethodSourceBuild, Source: need.SourceDir,
			Commands:            [][]string{{"go", "build", "-o", "<runtime>/bin/" + need.Name, "."}},
			Ownership:           OwnershipGrokinstall,
			Reversible:          true,
			Verification:        "run the built binary and check it starts",
			Requires:            requires,
			Risks:               risk,
			RequiresSourceBuild: true,
		})
	}

	// An ecosystem package manager is a real alternative, but running one can
	// execute project build backends and lifecycle scripts. It is offered so the
	// refusal can explain the choice, never so it can be taken automatically.
	ecosystem := need.Ecosystem
	if ecosystem == "" && need.SourceDir != "" {
		ecosystem = packageEcosystem(need.SourceDir)
	}
	if ecosystem != "" {
		switch ecosystem {
		case "python":
			out = append(out, Candidate{
				Method: MethodPackageManager, Source: "python (uv/pip)",
				Commands:         [][]string{{"uv", "pip", "install", "<project>"}},
				Ownership:        OwnershipGrokinstall,
				LifecycleScripts: true,
				Reversible:       true,
				Verification:     "import the installed package and check it starts",
				Requires:         []Authorization{AuthInstallScripts},
				Risks: []string{
					"Python installation may execute the project's build backend",
					"may resolve and run project-defined packaging code",
				},
			})
		case "cargo":
			out = append(out, Candidate{
				Method: MethodPackageManager, Source: "cargo",
				Commands:     [][]string{{"cargo", "install", "--path", "<project>"}},
				Ownership:    OwnershipGrokinstall,
				Reversible:   true,
				Verification: "run the installed command",
				Requires:     []Authorization{AuthInstallScripts},
				Risks:        []string{"cargo runs project build scripts"},
			})
		case "npm":
			out = append(out, Candidate{
				Method: MethodPackageManager, Source: "npm",
				Commands:         [][]string{{"npm", "install", "<project>"}},
				Ownership:        OwnershipSystem,
				LifecycleScripts: true,
				Reversible:       false,
				Verification:     "run the installed command",
				Requires:         []Authorization{AuthInstallScripts, AuthSystemPackageManager},
				Risks:            []string{"npm may run preinstall/postinstall scripts", "changes global package state"},
			})
		}
	}

	if len(out) == 0 {
		out = append(out, Candidate{Method: MethodUnresolved, Ownership: OwnershipNone})
	}
	return Compare(out)
}

func (e *Engine) releaseCandidate(ctx context.Context, need Need) (Candidate, bool) {
	rel, err := e.GitHub.LatestRelease(ctx, need.Owner, need.Repo)
	if err != nil {
		return Candidate{}, false
	}
	asset, ok := github.MatchAsset(rel, github.CurrentPlatform())
	if !ok {
		return Candidate{}, false
	}
	notes := []string{
		"downloads a published release artifact for this platform",
		"files stay inside the GrokInstall-owned runtime directory",
	}
	if _, found, _ := e.GitHub.DownloadChecksumFile(ctx, rel, asset.Name); !found {
		notes = append(notes, "upstream publishes no checksum for this artifact: it will be hashed, not verified against a published value")
	}
	// Downloading a published release artifact is the documented, verifiable
	// route: it is safe by default. Network access is inherent to the method,
	// not an extra risk class that needs its own approval.
	return Candidate{
		Method: MethodRelease, Source: "github.com/" + need.Owner + "/" + need.Repo,
		Version: rel.TagName, Asset: asset.Name,
		NetworkAccess: true, Ownership: OwnershipGrokinstall, Reversible: true,
		Verification: "launch the binary and confirm it starts",
		Notes:        notes,
	}, true
}

// Perform provisions the selected candidate into a staged directory. It never
// writes into the final runtime: the caller commits.
func (e *Engine) Perform(ctx context.Context, c Candidate, need Need, stageDir string) (*Result, error) {
	switch c.Method {
	case MethodExisting:
		return &Result{
			Method: MethodExisting, Source: c.Path, ExecutablePath: c.Path,
			Notes: []string{"pre-existing executable used as-is"},
		}, nil
	case MethodRelease:
		return e.performRelease(ctx, c, need, stageDir)
	case MethodSourceBuild:
		return e.performSourceBuild(ctx, c, need, stageDir)
	default:
		return nil, &Refusal{Reason: "method " + string(c.Method) + " is not implemented"}
	}
}

func (e *Engine) performRelease(ctx context.Context, c Candidate, need Need, stageDir string) (*Result, error) {
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		return nil, err
	}
	rel, err := e.GitHub.LatestRelease(ctx, need.Owner, need.Repo)
	if err != nil {
		return nil, err
	}
	asset, ok := github.MatchAsset(rel, github.CurrentPlatform())
	if !ok {
		return nil, fmt.Errorf("no compatible release asset for this platform")
	}
	downloadPath := filepath.Join(stageDir, "download", filepath.Base(asset.Name))
	actual, err := e.GitHub.Download(ctx, asset.URL, downloadPath)
	if err != nil {
		return nil, err
	}
	res := &Result{
		Method: MethodRelease, Source: "github.com/" + need.Owner + "/" + need.Repo,
		Version: rel.TagName, Asset: asset.Name, ActualChecksum: actual,
	}

	// Verify against a published checksum when one exists. Never fabricate.
	if sums, found, err := e.GitHub.DownloadChecksumFile(ctx, rel, asset.Name); err == nil && found {
		if want, ok := sums[asset.Name]; ok {
			res.PublishedChecksum = want
			if want == actual {
				res.ChecksumStatus = "verified"
			} else {
				return nil, fmt.Errorf("checksum mismatch for %s: published %s, downloaded %s", asset.Name, want, actual)
			}
		} else {
			res.ChecksumStatus = "unavailable-upstream"
			res.Notes = append(res.Notes, "the published checksum file does not list this asset")
		}
	} else {
		res.ChecksumStatus = "unavailable-upstream"
		res.Notes = append(res.Notes, "upstream publishes no checksum: the artifact was hashed but not verified against a published value")
	}

	unpackDir := filepath.Join(stageDir, "unpacked")
	if err := os.MkdirAll(unpackDir, 0o755); err != nil {
		return nil, err
	}
	report, err := github.ExtractArchive(downloadPath, unpackDir)
	if err != nil {
		return nil, err
	}
	binDir := filepath.Join(stageDir, "runtime", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return nil, err
	}
	executable := findExecutable(unpackDir, need.Name)
	if executable == "" {
		return nil, fmt.Errorf("no executable named %q was found in the release asset", need.Name)
	}
	dest := filepath.Join(binDir, filepath.Base(executable))
	if err := copyFile(executable, dest); err != nil {
		return nil, err
	}
	if err := os.Chmod(dest, 0o755); err != nil {
		return nil, err
	}
	res.ExecutablePath = dest
	res.Notes = append(res.Notes, fmt.Sprintf("extracted %d files from the release asset", report.Files))
	return res, nil
}

func (e *Engine) performSourceBuild(ctx context.Context, c Candidate, need Need, stageDir string) (*Result, error) {
	binDir := filepath.Join(stageDir, "runtime", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return nil, err
	}
	out := filepath.Join(binDir, need.Name)
	build, err := gobuild.Build(ctx, gobuild.Request{
		SourceDir:    need.SourceDir,
		Output:       out,
		AllowScripts: e.AllowSourceBuild,
	}, "go")
	if err != nil {
		var refusal *gobuild.Refusal
		if errors.As(err, &refusal) {
			out := &Refusal{Reason: refusal.Reason, Details: refusal.Details}
			if refusal.Required != "" {
				out.Required = []Authorization{Authorization(refusal.Required)}
			}
			return nil, out
		}
		return nil, err
	}
	return &Result{
		Method: MethodSourceBuild, Source: need.SourceDir, ActualChecksum: "",
		BuildCommand: build.Command, ExecutablePath: out,
		Notes: []string{"compiled from source with `go build` only; no project scripts were run"},
	}, nil
}

// Commit moves a staged runtime into its final owned location and records
// integrity hashes.
func Commit(reg *registry.Registry, capabilityName string, staged *Result) (runtimeDir string, meta Metadata, err error) {
	if staged.ExecutablePath == "" {
		return "", Metadata{}, fmt.Errorf("nothing to commit: no executable was produced")
	}
	runtimeDir = reg.RuntimePath(capabilityName)
	if err := os.RemoveAll(runtimeDir); err != nil {
		return "", Metadata{}, err
	}
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		return "", Metadata{}, err
	}
	relExec, relErr := filepath.Rel(filepath.Join(stageDirOf(staged.ExecutablePath), "runtime"), staged.ExecutablePath)
	if relErr != nil {
		relExec = filepath.Base(staged.ExecutablePath)
	}
	// Copy the executable into the owned runtime.
	dest := filepath.Join(runtimeDir, "bin", filepath.Base(staged.ExecutablePath))
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", Metadata{}, err
	}
	if err := copyFile(staged.ExecutablePath, dest); err != nil {
		return "", Metadata{}, err
	}
	if err := os.Chmod(dest, 0o755); err != nil {
		return "", Metadata{}, err
	}

	meta = Metadata{
		Schema: MetadataSchema, Capability: capabilityName, Source: staged.Source,
		Method: staged.Method, Version: staged.Version, Asset: staged.Asset,
		CreatedAt: time.Now().UTC(), Executable: filepath.Join("bin", filepath.Base(dest)),
		ChecksumStatus: staged.ChecksumStatus, Ownership: OwnershipGrokinstall,
		Notes: staged.Notes, Checksums: map[string]string{},
	}
	_ = relExec
	// Hash every file in the runtime so later audits can detect modification.
	walkErr := filepath.Walk(runtimeDir, func(p string, info os.FileInfo, werr error) error {
		if werr != nil || info.IsDir() {
			return werr
		}
		rel, rerr := filepath.Rel(runtimeDir, p)
		if rerr != nil {
			return rerr
		}
		sum, herr := hashFile(p)
		if herr != nil {
			return herr
		}
		meta.Checksums[filepath.ToSlash(rel)] = sum
		return nil
	})
	if walkErr != nil {
		return "", Metadata{}, walkErr
	}
	data, merr := json.MarshalIndent(meta, "", "  ")
	if merr != nil {
		return "", Metadata{}, merr
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, "metadata.json"), append(data, '\n'), 0o644); err != nil {
		return "", Metadata{}, err
	}
	return runtimeDir, meta, nil
}

// packageEcosystem reports which ecosystem a local source declares.
func packageEcosystem(dir string) string {
	has := func(name string) bool {
		_, err := os.Stat(filepath.Join(dir, name))
		return err == nil
	}
	switch {
	case has("pyproject.toml") || has("setup.py") || has("requirements.txt"):
		return "python"
	case has("package.json"):
		return "npm"
	case has("Cargo.toml"):
		return "cargo"
	}
	return ""
}

func stageDirOf(path string) string {
	// The staged executable lives at <stage>/runtime/bin/<name>.
	idx := strings.LastIndex(path, string(filepath.Separator)+"runtime"+string(filepath.Separator))
	if idx < 0 {
		return filepath.Dir(path)
	}
	return path[:idx]
}

func findExecutable(root, name string) string {
	found := ""
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if info.Mode()&0o111 == 0 && filepath.Base(p) != name {
			return nil
		}
		if filepath.Base(p) == name || (info.Mode()&0o111 != 0 && found == "") {
			if filepath.Base(p) == name {
				found = p
			}
		}
		return nil
	})
	return found
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

// EnsureGitHubClient returns a configured client.
func EnsureGitHubClient(token string) *github.Client { return github.NewClient(token) }

var _ = http.StatusOK
