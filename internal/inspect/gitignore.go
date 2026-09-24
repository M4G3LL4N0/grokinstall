package inspect

import (
	"os"
	"path"
	"path/filepath"
	"strings"
)

// ignoreRules is a deliberately small .gitignore subset: enough to keep build
// output and local noise out of an inspection, without pretending to implement
// the full gitignore language. Anything ambiguous is simply not ignored, so the
// only risk is extra files being inspected, never a missed exclusion the user
// explicitly asked for.
type ignoreRules struct {
	patterns []ignorePattern
}

type ignorePattern struct {
	pattern  string
	dirOnly  bool
	rootOnly bool
	negated  bool
	segments []string
}

func loadGitignore(root string) ignoreRules {
	data, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		return ignoreRules{}
	}
	var rules ignoreRules
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		p := ignorePattern{}
		if strings.HasPrefix(line, "!") {
			// Negation is out of scope: ignoring less is the safe direction.
			continue
		}
		if strings.HasSuffix(line, "/") {
			p.dirOnly = true
			line = strings.TrimSuffix(line, "/")
		}
		if strings.HasPrefix(line, "/") {
			p.rootOnly = true
			line = strings.TrimPrefix(line, "/")
		}
		if line == "" {
			continue
		}
		p.pattern = line
		p.segments = strings.Split(line, "/")
		rules.patterns = append(rules.patterns, p)
	}
	return rules
}

func (r ignoreRules) empty() bool { return len(r.patterns) == 0 }

func (r ignoreRules) match(rel string, isDir bool) bool {
	if len(r.patterns) == 0 {
		return false
	}
	rel = strings.TrimPrefix(filepath.ToSlash(rel), "./")
	for _, p := range r.patterns {
		if p.dirOnly && !isDir {
			continue
		}
		if p.rootOnly {
			if p.matchExact(rel) {
				return true
			}
			continue
		}
		if p.matchExact(rel) {
			return true
		}
		// A pattern without a slash matches at any depth.
		if len(p.segments) == 1 {
			if p.matchBase(rel) {
				return true
			}
		}
		// A path pattern matches any prefix of the path.
		if p.matchPrefix(rel) {
			return true
		}
	}
	return false
}

func (p ignorePattern) matchExact(rel string) bool {
	if p.pattern == rel {
		return true
	}
	ok, err := path.Match(p.pattern, rel)
	return err == nil && ok
}

func (p ignorePattern) matchBase(rel string) bool {
	base := path.Base(rel)
	ok, err := path.Match(p.pattern, base)
	return err == nil && ok
}

func (p ignorePattern) matchPrefix(rel string) bool {
	parts := strings.Split(rel, "/")
	for i := range parts {
		candidate := strings.Join(parts[:i+1], "/")
		ok, err := path.Match(p.pattern, candidate)
		if err == nil && ok {
			return true
		}
	}
	return false
}
