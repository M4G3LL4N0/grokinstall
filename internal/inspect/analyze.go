package inspect

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/M4G3LL4N0/grokinstall/internal/cache"
	"github.com/M4G3LL4N0/grokinstall/internal/source"
)

// GitRunner abstracts git so tests can simulate a missing or failing git.
type GitRunner interface {
	Available() bool
	Output(ctx context.Context, dir string, args ...string) (string, error)
}

// RealGit invokes the git binary found on PATH.
type RealGit struct{}

// Available reports whether git is on PATH.
func (RealGit) Available() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// Output runs git in dir and returns stdout.
func (RealGit) Output(ctx context.Context, dir string, args ...string) (string, error) {
	bin, err := exec.LookPath("git")
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	// Keep clone output inert: never let git evaluate repository content.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.Output()
	return string(out), err
}

func contextTODO() context.Context { return context.Background() }

func osReadFile(path string) ([]byte, error) { return os.ReadFile(path) }

type fileInfo struct{ size int64 }

func statFile(root, rel string) (fileInfo, error) {
	fi, err := os.Stat(filepath.Join(root, rel))
	if err != nil {
		return fileInfo{}, err
	}
	return fileInfo{size: fi.Size()}, nil
}

// Identity returns a stable identity for a source: a commit SHA for git
// repositories, otherwise a hash of the inspected tree.
func Identity(ctx context.Context, src source.Source, opts Options) (string, error) {
	opts = opts.normalized()
	switch src.Kind {
	case source.KindLocalDir:
		if commit := commitForDir(src.LocalPath, opts.Git); commit != "" {
			return "local:" + src.LocalPath + "@" + commit, nil
		}
		res, err := Inspect(src.LocalPath, opts)
		if err != nil {
			return "", err
		}
		return res.Identity, nil
	case source.KindGitHub:
		commit, err := resolveRemoteCommit(ctx, src, opts.Git)
		if err != nil {
			return "", err
		}
		return "github:" + src.Canonical + "@" + commit, nil
	default:
		return "", fmt.Errorf("cannot compute identity for source kind %q", src.Kind)
	}
}

func resolveRemoteCommit(ctx context.Context, src source.Source, g GitRunner) (string, error) {
	if !g.Available() {
		return "", fmt.Errorf("git is required to inspect %s repositories but was not found on PATH", src.Kind)
	}
	out, err := g.Output(ctx, "", "ls-remote", src.URL, "HEAD")
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", src.URL, err)
	}
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return "", fmt.Errorf("resolve %s: empty ls-remote response", src.URL)
	}
	return fields[0], nil
}

// Analyze inspects a source, serving deterministic results from the cache when
// the source identity, inspection version and options are unchanged.
func Analyze(ctx context.Context, src source.Source, store *cache.Store, opts Options) (*Result, error) {
	opts = opts.normalized()
	switch src.Kind {
	case source.KindLocalDir:
		return analyzeLocal(ctx, src, store, opts)
	case source.KindGitHub:
		return analyzeGitHub(ctx, src, store, opts)
	default:
		return nil, fmt.Errorf("source kind %q is not supported in Part 1: provide a local directory or a public GitHub repository", src.Kind)
	}
}

func cacheKey(src source.Source, identity string, opts Options) cache.Key {
	return cache.Key{
		Namespace: "inspection",
		Identity:  identity,
		Version:   InspectionVersion + ":" + fingerprint(opts),
	}
}

func fingerprint(opts Options) string {
	gitKind := "real"
	if _, ok := opts.Git.(RealGit); !ok {
		gitKind = "custom"
	}
	return fmt.Sprintf("f=%d;t=%d;m=%d;g=%s", opts.MaxFileBytes, opts.MaxTotalBytes, opts.MaxFiles, gitKind)
}

func analyzeLocal(ctx context.Context, src source.Source, store *cache.Store, opts Options) (*Result, error) {
	identity, err := Identity(ctx, src, opts)
	if err != nil {
		return nil, err
	}
	key := cacheKey(src, identity, opts)
	if store != nil {
		if data, hit, err := store.Get(key); err == nil && hit {
			var res Result
			if json.Unmarshal(data, &res) == nil {
				res.CacheHit = true
				res.Source = src
				return &res, nil
			}
		}
	}
	res, err := Inspect(src.LocalPath, opts)
	if err != nil {
		return nil, err
	}
	res.Source = src
	res.Identity = identity
	if store != nil {
		if data, err := json.Marshal(res); err == nil {
			_ = store.Put(key, data)
		}
	}
	return res, nil
}

func analyzeGitHub(ctx context.Context, src source.Source, store *cache.Store, opts Options) (*Result, error) {
	commit, err := resolveRemoteCommit(ctx, src, opts.Git)
	if err != nil {
		return nil, err
	}
	identity := "github:" + src.Canonical + "@" + commit
	key := cacheKey(src, identity, opts)
	if store != nil {
		if data, hit, err := store.Get(key); err == nil && hit {
			var res Result
			if json.Unmarshal(data, &res) == nil {
				res.CacheHit = true
				res.Source = src
				return &res, nil
			}
		}
	}
	if store == nil {
		return nil, fmt.Errorf("inspecting %s requires a state directory for controlled clone storage", src.Canonical)
	}
	work := filepath.Join(store.WorkDir(), strings.ReplaceAll(src.Canonical, "/", "-")+"-"+commit[:12])
	if err := os.MkdirAll(work, 0o755); err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)

	// Clone without executing anything from the repository.
	if _, err := opts.Git.Output(ctx, "", "clone", "--depth", "1", "--quiet", src.URL, work); err != nil {
		return nil, fmt.Errorf("clone %s: %w", src.URL, err)
	}
	res, err := Inspect(work, opts)
	if err != nil {
		return nil, err
	}
	res.Source = src
	res.Identity = identity
	res.CommitSHA = commit
	if data, err := json.Marshal(res); err == nil {
		_ = store.Put(key, data)
	}
	return res, nil
}
