// Package source normalizes a user-supplied SOURCE reference into a canonical
// identity that the rest of the pipeline can work with.
//
// Part 1 supports local directories and public GitHub repositories. Other
// remote hosts are recognized but reported as unsupported rather than guessed.
package source

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Kind identifies the normalized source type.
type Kind string

const (
	// KindLocalDir is a directory on the local filesystem.
	KindLocalDir Kind = "local_dir"
	// KindGitHub is a public GitHub repository.
	KindGitHub Kind = "github"
	// KindGitLab is recognized but not supported in Part 1.
	KindGitLab Kind = "gitlab"
	// KindUnknown could not be classified.
	KindUnknown Kind = "unknown"
)

// Source is a normalized SOURCE reference.
type Source struct {
	Ref       string `json:"ref"`
	Kind      Kind   `json:"kind"`
	LocalPath string `json:"local_path,omitempty"`
	Owner     string `json:"owner,omitempty"`
	Repo      string `json:"repo,omitempty"`
	URL       string `json:"url,omitempty"`
	Canonical string `json:"canonical"`
}

// ErrUnsupported reports a recognized source that Part 1 does not support.
var ErrUnsupported = errors.New("unsupported source for this version")

// Normalize turns a SOURCE argument into a Source.
func Normalize(ref string) (*Source, error) {
	if ref == "" {
		return nil, errors.New("empty source; provide a local path or a repository URL")
	}
	if s, ok, err := parseRemote(ref); ok || err != nil {
		if err != nil {
			return nil, err
		}
		return s, nil
	}
	return normalizeLocal(ref)
}

func parseRemote(ref string) (*Source, bool, error) {
	var host string
	var path string

	lower := strings.ToLower(ref)
	switch {
	case strings.HasPrefix(lower, "https://github.com/"), strings.HasPrefix(lower, "http://github.com/"):
		host, path = "github.com", ref[strings.Index(lower, "://")+3+len("github.com"):]
	case strings.HasPrefix(ref, "git@github.com:"):
		host, path = "github.com", strings.TrimPrefix(ref, "git@github.com:")
	case strings.HasPrefix(lower, "https://gitlab.com/"), strings.HasPrefix(lower, "http://gitlab.com/"):
		host, path = "gitlab.com", ref[strings.Index(lower, "://")+3+len("gitlab.com"):]
	default:
		return nil, false, nil
	}

	if host == "" {
		return nil, false, nil
	}

	path = strings.TrimSuffix(path, ".git")
	path = strings.Trim(path, "/")
	segs := strings.Split(path, "/")
	segs = stripLowerSegments(segs)

	switch host {
	case "github.com":
		if len(segs) < 2 {
			return nil, true, fmt.Errorf("invalid GitHub source %q: expected <owner>/<repo>", ref)
		}
		s := &Source{
			Ref:       ref,
			Kind:      KindGitHub,
			Owner:     segs[0],
			Repo:      segs[1],
			URL:       fmt.Sprintf("https://github.com/%s/%s", segs[0], segs[1]),
			Canonical: fmt.Sprintf("github.com/%s/%s", segs[0], segs[1]),
		}
		return s, true, nil
	case "gitlab.com":
		if len(segs) < 2 {
			return nil, true, fmt.Errorf("invalid GitLab source %q: expected <owner>/<repo>", ref)
		}
		s := &Source{
			Ref:       ref,
			Kind:      KindGitLab,
			Owner:     segs[0],
			Repo:      segs[1],
			URL:       fmt.Sprintf("https://gitlab.com/%s/%s", segs[0], segs[1]),
			Canonical: fmt.Sprintf("gitlab.com/%s/%s", segs[0], segs[1]),
		}
		return s, true, nil
	default:
		return nil, false, nil
	}
}

// stripLowerSegments drops URL segments after the repo name (tree, blob,
// branches, tags, release paths) that carry no repo identity.
func stripLowerSegments(segs []string) []string {
	if len(segs) <= 2 {
		return segs
	}
	for i, seg := range segs {
		switch strings.ToLower(seg) {
		case "tree", "blob", "commit", "releases", "archive", "issues", "actions", "tags", "wiki":
			return segs[:i]
		}
		if i > 1 {
			return segs[:2]
		}
	}
	return segs[:2]
}

func normalizeLocal(ref string) (*Source, error) {
	p := ref
	if strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve home directory: %w", err)
		}
		p = filepath.Join(home, strings.TrimPrefix(p, "~/"))
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return nil, fmt.Errorf("resolve path %q: %w", ref, err)
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("source %q is not accessible: %w", ref, err)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("source %q is a file, not a directory; point at a project directory", ref)
	}
	return &Source{
		Ref:       ref,
		Kind:      KindLocalDir,
		LocalPath: abs,
		Canonical: "path:" + abs,
	}, nil
}
