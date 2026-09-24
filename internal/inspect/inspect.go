// Package inspect reads a source deterministically and records evidence.
//
// Inspection never executes project code: no install scripts, no build steps,
// no package managers. Repository text is treated as untrusted data, and every
// conclusion is recorded as a finding with the evidence that produced it.
package inspect

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"grokinstall/internal/evidence"
	"grokinstall/internal/source"
)

// Schema identifies an inspection result.
const Schema = "grokinstall/inspection/v1"

// InspectionVersion participates in the cache key. Bump it when the meaning of
// an inspection changes so stale results are never reused.
const InspectionVersion = "v1"

// Default bounds. Inspection is deliberately shallow: enough signal to plan an
// integration, not enough to copy a repository into a model context.
const (
	DefaultMaxFileBytes  int64 = 512 * 1024
	DefaultMaxTotalBytes int64 = 32 * 1024 * 1024
	DefaultMaxFiles      int   = 8000
)

// FileRole classifies a file for evidence and planning.
type FileRole string

// File roles.
const (
	RoleManifest   FileRole = "manifest"
	RoleLockfile   FileRole = "lockfile"
	RoleReadme     FileRole = "readme"
	RoleDocs       FileRole = "docs"
	RoleDocker     FileRole = "docker"
	RoleOpenAPI    FileRole = "openapi"
	RoleMCP        FileRole = "mcp"
	RoleExecutable FileRole = "executable"
	RoleTests      FileRole = "tests"
	RoleWorkflow   FileRole = "workflow"
	RoleLicense    FileRole = "license"
	RoleEnvExample FileRole = "env_example"
	RoleMakefile   FileRole = "makefile"
	RoleService    FileRole = "service"
	RoleOther      FileRole = "other"
)

// File is one recorded file with its role and size.
type File struct {
	Path string   `json:"path"`
	Role FileRole `json:"role"`
	Size int64    `json:"size"`
}

// Meta is the small factual summary of a source.
type Meta struct {
	Name        string   `json:"name,omitempty"`
	Version     string   `json:"version,omitempty"`
	Description string   `json:"description,omitempty"`
	Languages   []string `json:"languages,omitempty"`
	Entrypoints []string `json:"entrypoints,omitempty"`
	// EntrypointPaths are where declared commands actually live, so an
	// installer can invoke them without guessing.
	EntrypointPaths []string `json:"entrypoint_paths,omitempty"`
	Dependencies    []string `json:"dependencies,omitempty"`
}

// SecurityReport summarizes what inspection saw that matters for safety.
type SecurityReport struct {
	InstallScripts   []string `json:"install_scripts,omitempty"`
	PromptInjection  bool     `json:"prompt_injection_detected"`
	SecretLikeFiles  []string `json:"secret_like_files,omitempty"`
	NetworkDownloads []string `json:"network_download_scripts,omitempty"`
	Privileged       []string `json:"privileged_commands,omitempty"`
	Notes            []string `json:"notes,omitempty"`
}

// Result is the complete output of inspection.
type Result struct {
	Schema      string          `json:"schema"`
	Source      source.Source   `json:"source"`
	Root        string          `json:"root"`
	Identity    string          `json:"identity"`
	CommitSHA   string          `json:"commit_sha,omitempty"`
	Files       []File          `json:"files"`
	Meta        Meta            `json:"meta"`
	Evidence    []evidence.Item `json:"evidence"`
	Security    SecurityReport  `json:"security"`
	Truncated   bool            `json:"truncated"`
	Warnings    []string        `json:"warnings,omitempty"`
	InspectedAt time.Time       `json:"inspected_at"`
	CacheHit    bool            `json:"cache_hit,omitempty"`
}

// Has reports whether a finding exists in the result.
func (r *Result) Has(finding string) bool {
	for _, it := range r.Evidence {
		if it.Finding == finding {
			return true
		}
	}
	return false
}

// EvidenceSet converts the recorded evidence into a queryable set.
func (r *Result) EvidenceSet() *evidence.Set {
	s := evidence.New()
	for _, it := range r.Evidence {
		s.AddItem(it)
	}
	return s
}

// Options bounds an inspection.
type Options struct {
	MaxFileBytes  int64
	MaxTotalBytes int64
	MaxFiles      int
	Git           GitRunner
}

func (o Options) normalized() Options {
	if o.MaxFileBytes <= 0 {
		o.MaxFileBytes = DefaultMaxFileBytes
	}
	if o.MaxTotalBytes <= 0 {
		o.MaxTotalBytes = DefaultMaxTotalBytes
	}
	if o.MaxFiles <= 0 {
		o.MaxFiles = DefaultMaxFiles
	}
	if o.Git == nil {
		o.Git = RealGit{}
	}
	return o
}

// skippedDirs are never walked: dependency trees, build output and test
// fixtures are not part of the surface GrokInstall integrates.
var skippedDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, ".venv": true, "venv": true,
	"dist": true, "build": true, "target": true, ".next": true, ".nuxt": true,
	"coverage": true, "__pycache__": true, ".mypy_cache": true, ".pytest_cache": true,
	".tox": true, "site-packages": true, ".gradle": true, ".idea": true,
	".terraform": true, "Pods": true, ".cache": true, "testdata": true,
}

// Inspect reads root and produces a Result without caching it.
func Inspect(root string, opts Options) (*Result, error) {
	opts = opts.normalized()
	fi, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("inspect %q: %w", root, err)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("inspect %q: not a directory", root)
	}

	src := source.Source{Kind: source.KindLocalDir, LocalPath: root, Ref: root, Canonical: "path:" + root}
	res := &Result{
		Schema:      Schema,
		Source:      src,
		Root:        root,
		InspectedAt: time.Now().UTC(),
	}

	ev := evidence.New()
	security := SecurityReport{Notes: []string{"inspection never executes project code"}}

	scan := &scanner{root: root, opts: opts, ev: ev, res: res, security: &security}
	scan.ignore = loadGitignore(root)
	if err := scan.walk(); err != nil {
		return nil, err
	}
	if scan.commit != "" {
		res.CommitSHA = scan.commit
	}

	sortFiles(res.Files)
	parseManifests(scan)

	res.Evidence = ev.List()
	res.Security = security
	res.Identity = identityFromTree(scan)
	res.Meta.finalize()
	return res, nil
}

type scanner struct {
	root     string
	opts     Options
	ev       *evidence.Set
	res      *Result
	security *SecurityReport
	commit   string
	total    int64
	files    int
	hashes   []string
	ignore   ignoreRules
}

func (s *scanner) walk() error {
	commit := commitForDir(s.root, s.opts.Git)
	s.commit = commit
	if commit != "" {
		s.ev.Add("git_repository", evidence.ConfidenceHigh, ".git present at commit "+commit)
	}

	return filepath.WalkDir(s.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			s.ev.Add("unreadable_path", evidence.ConfidenceLow, relPath(s.root, path))
			return nil
		}
		rel := relPath(s.root, path)
		if d.IsDir() {
			if rel == "." {
				return nil
			}
			if skippedDirs[d.Name()] {
				return filepath.SkipDir
			}
			if s.ignore.match(rel, true) {
				return filepath.SkipDir
			}
			return nil
		}
		if s.ignore.match(rel, false) {
			return nil
		}
		if s.files >= s.opts.MaxFiles {
			s.res.Truncated = true
			s.ev.Add("size_limit_hit", evidence.ConfidenceHigh, "file count limit reached")
			return filepath.SkipAll
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			s.ev.Add("symlink_present", evidence.ConfidenceLow, rel+" (not followed)")
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		s.files++
		s.total += info.Size()
		role := classifyFile(rel, fsFileInfo{mode: uint32(info.Mode())})
		s.res.Files = append(s.res.Files, File{Path: rel, Role: role, Size: info.Size()})
		s.hashFile(rel, info)
		lang := detectLanguage(rel)
		if lang != "" {
			s.res.Meta.Languages = append(s.res.Meta.Languages, lang)
		}
		if info.Size() > s.opts.MaxFileBytes {
			s.res.Truncated = true
			s.ev.Add("size_limit_hit", evidence.ConfidenceHigh,
				fmt.Sprintf("%s exceeds per-file limit (%d bytes); contents not parsed", rel, info.Size()))
			return nil
		}
		if s.total > s.opts.MaxTotalBytes {
			s.res.Truncated = true
			s.ev.Add("size_limit_hit", evidence.ConfidenceHigh, "total inspection byte budget reached")
			return filepath.SkipAll
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			s.ev.Add("unreadable_file", evidence.ConfidenceLow, rel)
			return nil
		}
		s.analyze(rel, role, data)
		return nil
	})
}

func (s *scanner) hashFile(rel string, info fs.FileInfo) {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|", rel, info.Size())))
	s.hashes = append(s.hashes, rel+"|"+fmt.Sprint(info.Size())+"|"+hex.EncodeToString(sum[:]))
}

func relPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}

func sortFiles(files []File) {
	sort.SliceStable(files, func(i, j int) bool { return files[i].Path < files[j].Path })
}

func identityFromTree(s *scanner) string {
	if s.commit != "" {
		return "local:" + s.root + "@" + s.commit
	}
	sum := sha256.Sum256([]byte(strings.Join(s.hashes, "\n")))
	return "local:" + s.root + "#" + hex.EncodeToString(sum[:])[:32]
}

func commitForDir(dir string, g GitRunner) string {
	if !g.Available() {
		return ""
	}
	out, err := g.Output(contextTODO(), dir, "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// Add records evidence, kept small for the walker to reuse.
func (s *scanner) add(finding string, conf evidence.Confidence, ev ...string) {
	s.ev.Add(finding, conf, ev...)
}

// fsFileInfo is the minimal file information classification needs.
type fsFileInfo struct{ mode uint32 }

func (f fsFileInfo) Mode() uint32 { return f.mode }

func detectLanguage(rel string) string {
	ext := strings.ToLower(filepath.Ext(rel))
	switch ext {
	case ".go":
		return "go"
	case ".js", ".mjs", ".cjs", ".ts", ".tsx", ".jsx":
		return "javascript"
	case ".py":
		return "python"
	case ".rs":
		return "rust"
	}
	return ""
}
