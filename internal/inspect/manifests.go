package inspect

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"grokinstall/internal/evidence"
)

// parseManifests walks recorded files and extracts facts from the manifests
// that describe a project's shape. Only a manifest at the repository root
// defines the project's identity; nested manifests (examples, submodules,
// vendored fixtures) contribute evidence but never overwrite who the project is.
func parseManifests(s *scanner) {
	for _, f := range s.res.Files {
		if f.Role != RoleManifest && f.Role != RoleLockfile {
			continue
		}
		base := strings.ToLower(filepath.Base(f.Path))
		data, err := readLimited(s.root, f.Path, s.opts.MaxFileBytes)
		if err != nil {
			continue
		}
		isRoot := !strings.Contains(f.Path, "/")
		if !isRoot {
			s.add("nested_manifest", evidence.ConfidenceLow,
				f.Path+" is a nested manifest; it does not define the project identity")
		}
		switch base {
		case "package.json":
			s.parsePackageJSON(f.Path, data, isRoot)
		case "pyproject.toml":
			s.parsePyproject(f.Path, data, isRoot)
		case "requirements.txt":
			s.parseRequirements(f.Path, data)
		case "cargo.toml":
			s.parseCargo(f.Path, data, isRoot)
		case "go.mod":
			s.parseGoMod(f.Path, data, isRoot)
		case "pnpm-lock.yaml", "package-lock.json", "yarn.lock", "poetry.lock", "uv.lock", "cargo.lock":
			s.add("lockfile_present", evidence.ConfidenceHigh, f.Path+" pins dependency versions")
		}
	}
}

func readLimited(root, rel string, max int64) ([]byte, error) {
	fi, err := statFile(root, rel)
	if err != nil {
		return nil, err
	}
	if fi.size > max {
		return nil, fmt.Errorf("file too large")
	}
	return osReadFile(filepath.Join(root, rel))
}

type packageJSON struct {
	Name            string            `json:"name"`
	Version         string            `json:"version"`
	Description     string            `json:"description"`
	Main            string            `json:"main"`
	Scripts         map[string]string `json:"scripts"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
	Keywords        []string          `json:"keywords"`
	License         string            `json:"license"`
	Workspaces      json.RawMessage   `json:"workspaces"`
}

func (s *scanner) parsePackageJSON(rel string, data []byte, isRoot bool) {
	var p packageJSON
	if err := json.Unmarshal(data, &p); err != nil {
		s.add("manifest_parse_error", evidence.ConfidenceHigh, rel+" is not valid JSON: "+err.Error())
		return
	}
	s.add("javascript_project", evidence.ConfidenceHigh, rel+" is a package.json manifest")
	setMeta(&s.res.Meta, p.Name, p.Version, p.Description, isRoot)
	for dep := range p.Dependencies {
		s.res.Meta.Dependencies = append(s.res.Meta.Dependencies, dep)
	}
	// bin may be a string or a map; both declare CLI entrypoints.
	extractBin(data, s, rel)
	s.recordInstallScripts(p.Scripts, rel)
	if _, ok := p.Scripts["start"]; ok {
		s.add("local_service_hint", evidence.ConfidenceLow, rel+" defines a start script")
	}
	if len(p.Keywords) > 0 {
		s.add("keywords_present", evidence.ConfidenceLow, rel+" declares keywords: "+strings.Join(p.Keywords, ", "))
	}
}

func extractBin(data []byte, s *scanner, rel string) {
	var probe struct {
		Bin json.RawMessage `json:"bin"`
	}
	if err := json.Unmarshal(data, &probe); err != nil || len(probe.Bin) == 0 {
		return
	}
	var binMap map[string]string
	if err := json.Unmarshal(probe.Bin, &binMap); err == nil {
		for name := range binMap {
			s.res.Meta.Entrypoints = append(s.res.Meta.Entrypoints, name)
			s.add("cli_entrypoint", evidence.ConfidenceHigh,
				fmt.Sprintf("%s bin field declares command %q", rel, name))
		}
		return
	}
	var binStr string
	if err := json.Unmarshal(probe.Bin, &binStr); err == nil && binStr != "" {
		s.res.Meta.Entrypoints = append(s.res.Meta.Entrypoints, filepath.Base(binStr))
		s.add("cli_entrypoint", evidence.ConfidenceHigh, rel+" bin field declares a command")
	}
}

var tomlSectionRe = regexp.MustCompile(`^\s*\[([^\]]+)\]\s*$`)
var tomlKeyRe = regexp.MustCompile(`^\s*([A-Za-z0-9_.-]+)\s*=\s*(.+?)\s*$`)

func (s *scanner) parsePyproject(rel string, data []byte, isRoot bool) {
	s.add("python_project", evidence.ConfidenceHigh, rel+" is a pyproject.toml manifest")
	section := ""
	inScripts := false
	for _, line := range strings.Split(string(data), "\n") {
		if m := tomlSectionRe.FindStringSubmatch(line); m != nil {
			section = strings.TrimSpace(m[1])
			inScripts = strings.HasSuffix(section, "scripts")
			continue
		}
		m := tomlKeyRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		key, val := m[1], unquoteTOML(m[2])
		if inScripts {
			s.res.Meta.Entrypoints = append(s.res.Meta.Entrypoints, key)
			s.add("cli_entrypoint", evidence.ConfidenceHigh, rel+" declares console script "+key)
			continue
		}
		switch section + "." + key {
		case "project.name":
			setMetaField(&s.res.Meta, "name", val, isRoot)
		case "project.version":
			setMetaField(&s.res.Meta, "version", val, isRoot)
		case "project.description":
			setMetaField(&s.res.Meta, "description", val, isRoot)
		}
		if section == "project" && key == "dependencies" {
			s.res.Meta.Dependencies = append(s.res.Meta.Dependencies, val)
		}
	}
}

func (s *scanner) parseRequirements(rel string, data []byte) {
	s.add("python_project", evidence.ConfidenceHigh, rel+" pins Python requirements")
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "-") {
			continue
		}
		name := strings.FieldsFunc(line, func(r rune) bool {
			return r == '=' || r == '>' || r == '<' || r == '!' || r == '~' || r == '[' || r == ' '
		})
		if len(name) > 0 {
			s.res.Meta.Dependencies = append(s.res.Meta.Dependencies, name[0])
		}
	}
}

func (s *scanner) parseCargo(rel string, data []byte, isRoot bool) {
	s.add("rust_project", evidence.ConfidenceHigh, rel+" is a Cargo.toml manifest")
	section := ""
	for _, line := range strings.Split(string(data), "\n") {
		if m := tomlSectionRe.FindStringSubmatch(line); m != nil {
			section = strings.TrimSpace(m[1])
			continue
		}
		m := tomlKeyRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		key, val := m[1], unquoteTOML(m[2])
		if section == "package" {
			switch key {
			case "name":
				setMetaField(&s.res.Meta, "name", val, isRoot)
			case "version":
				setMetaField(&s.res.Meta, "version", val, isRoot)
			case "description":
				setMetaField(&s.res.Meta, "description", val, isRoot)
			}
		}
		if section == "bin" && key == "name" {
			s.res.Meta.Entrypoints = append(s.res.Meta.Entrypoints, val)
			s.add("cli_entrypoint", evidence.ConfidenceHigh, rel+" declares binary "+val)
		}
		if section == "dependencies" {
			s.res.Meta.Dependencies = append(s.res.Meta.Dependencies, key)
		}
	}
}

var goRequireRe = regexp.MustCompile(`^\s*(\S+)\s+v\S+`)

func (s *scanner) parseGoMod(rel string, data []byte, isRoot bool) {
	s.add("go_project", evidence.ConfidenceHigh, rel+" is a Go module manifest")
	inRequire := false
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "module ") {
			modPath := strings.TrimSpace(strings.TrimPrefix(trimmed, "module "))
			setMetaField(&s.res.Meta, "name", moduleDisplayName(modPath), isRoot)
			s.add("go_module_path", evidence.ConfidenceHigh, rel+" declares module "+modPath)
		}
		if strings.HasPrefix(trimmed, "go ") {
			s.add("go_version_declared", evidence.ConfidenceHigh, rel+" declares go "+strings.TrimSpace(strings.TrimPrefix(trimmed, "go ")))
		}
		if strings.HasPrefix(trimmed, "require (") {
			inRequire = true
			continue
		}
		if inRequire {
			if trimmed == ")" {
				inRequire = false
				continue
			}
			if m := goRequireRe.FindStringSubmatch(trimmed); m != nil {
				s.res.Meta.Dependencies = append(s.res.Meta.Dependencies, m[1])
			}
		}
	}
}

func moduleDisplayName(modPath string) string {
	if idx := strings.LastIndex(modPath, "/"); idx >= 0 {
		return modPath[idx+1:]
	}
	return modPath
}

// setMeta records project identity. Only a root-level manifest defines it: a
// nested package's name or version describes that package, not the project, so
// allowing it to fill gaps would misreport what is being integrated.
func setMeta(m *Meta, name, version, desc string, isRoot bool) {
	if !isRoot {
		return
	}
	if name != "" {
		m.Name = name
	}
	if version != "" {
		m.Version = version
	}
	if desc != "" {
		m.Description = desc
	}
}

func setMetaField(m *Meta, field, value string, isRoot bool) {
	if !isRoot || value == "" {
		return
	}
	switch field {
	case "name":
		m.Name = value
	case "version":
		m.Version = value
	case "description":
		m.Description = value
	}
}

func unquoteTOML(v string) string {
	v = strings.TrimSpace(v)
	v = strings.Trim(v, "\"'")
	if idx := strings.Index(v, " #"); idx >= 0 {
		v = strings.TrimSpace(v[:idx])
	}
	return v
}
