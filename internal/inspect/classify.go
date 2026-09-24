package inspect

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"grokinstall/internal/evidence"
)

var openAPIFilenames = []string{"openapi.json", "openapi.yaml", "openapi.yml", "swagger.json", "swagger.yaml", "swagger.yml", "api.json", "api.yaml", "api.yml"}

func classifyFile(rel string, info fsFileInfo) FileRole {
	base := path.Base(rel)
	lower := strings.ToLower(base)
	ext := strings.ToLower(path.Ext(base))

	switch {
	case lower == "readme" || strings.HasPrefix(lower, "readme."):
		return RoleReadme
	case lower == "package.json", lower == "pyproject.toml", lower == "requirements.txt",
		lower == "cargo.toml", lower == "go.mod":
		return RoleManifest
	case lower == "pnpm-lock.yaml", lower == "package-lock.json", lower == "yarn.lock",
		lower == "poetry.lock", lower == "uv.lock", lower == "cargo.lock", lower == "go.sum":
		return RoleLockfile
	case lower == "dockerfile" || strings.HasPrefix(lower, "dockerfile."):
		return RoleDocker
	case strings.HasPrefix(lower, "docker-compose") || lower == "compose.yml" || lower == "compose.yaml":
		return RoleDocker
	case isOpenAPIName(lower):
		return RoleOpenAPI
	case lower == ".mcp.json" || lower == "mcp.json" || strings.HasSuffix(lower, ".mcp.json"):
		return RoleMCP
	case lower == "makefile" || lower == "gnumakefile" || lower == "makefile.am":
		return RoleMakefile
	case lower == "license" || strings.HasPrefix(lower, "license.") || lower == "copying":
		return RoleLicense
	case isEnvExample(lower):
		return RoleEnvExample
	case isWorkflow(rel):
		return RoleWorkflow
	case info.Mode()&0o111 != 0:
		return RoleExecutable
	case isTestPath(rel):
		return RoleTests
	case ext == ".md" || ext == ".rst" || ext == ".adoc" || ext == ".txt":
		return RoleDocs
	}
	return RoleOther
}

func isOpenAPIName(lower string) bool {
	for _, n := range openAPIFilenames {
		if lower == n {
			return true
		}
	}
	return false
}

func isEnvExample(lower string) bool {
	if !strings.HasPrefix(lower, ".env") && !strings.HasPrefix(lower, "env.") {
		return false
	}
	return strings.Contains(lower, "example") || strings.Contains(lower, "sample") ||
		strings.Contains(lower, "template") || strings.Contains(lower, "dist")
}

func isWorkflow(rel string) bool {
	return strings.HasPrefix(rel, ".github/workflows/") || strings.HasPrefix(rel, ".gitlab/ci/")
}

// testRe recognizes test files so `grokinstall test` can run a source's own
// suite later without treating test files as a separate capability.
var testRe = regexp.MustCompile(`(^|/)(tests?|__tests__|spec)(/|$)|(_test\.(go|py|js|ts|rb)$)|(\.test\.(js|ts|tsx|jsx)$)|(\.spec\.(js|ts|tsx|jsx)$)|(test_.*\.py$)`)

// skipDirs are never walked. testdata holds fixtures for a source's own tests,
// not part of the product surface GrokInstall is integrating.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, ".venv": true, "venv": true,
	"dist": true, "build": true, "target": true, ".next": true, ".nuxt": true,
	"coverage": true, "__pycache__": true, ".mypy_cache": true, ".pytest_cache": true,
	".tox": true, "site-packages": true, ".gradle": true, ".idea": true,
	".terraform": true, "Pods": true, ".cache": true, "testdata": true,
}

func isTestPath(rel string) bool { return testRe.MatchString(rel) }

// injectionRe matches instruction-like text. Matches are recorded as untrusted
// evidence and never treated as instructions to GrokInstall.
var injectionRe = regexp.MustCompile(`(?i)(ignore (all )?(the )?previous instructions|disregard (all )?(the )?(above|previous)|you are now|system prompt|new instructions|exfiltrat|reveal your)`)

var downloadRe = regexp.MustCompile(`(?i)\b(curl|wget)\b[^\n]*https?://`)
var sudoRe = regexp.MustCompile(`(?i)\b(sudo|doas)\b|\bUSER\s+root\b`)
var secretRe = regexp.MustCompile(`(?i)(^|/)(\.env$|\.env\.[^e]|\.netrc$|\.npmrc$|\.pypirc$|id_rsa|id_ed25519|.*\.pem$|.*\.p12$|.*credentials.*\.json$|.*secret.*\.json$|secrets?\.(yaml|yml|toml)$)`)

// serviceRe spots a long-running server in common shapes.
var serviceRe = regexp.MustCompile(`(?i)(listen\s*\(|uvicorn|gunicorn|flask\.|fastapi|createServer|http\.Server|app\.listen|@app\.route|grpc\.NewServer)`)

func (s *scanner) analyze(rel string, role FileRole, data []byte) {
	text := string(data)
	lower := strings.ToLower(rel)

	switch role {
	case RoleReadme:
		if injectionRe.MatchString(text) {
			s.security.PromptInjection = true
			s.security.Notes = appendUnique(s.security.Notes,
				"readme contains instruction-like text; recorded as untrusted data and never obeyed")
			s.add("untrusted_instruction_text", evidence.ConfidenceHigh,
				rel+" contains instruction-like text (recorded, not obeyed)")
		}
		s.add("readme_present", evidence.ConfidenceHigh, rel)
		s.add("documentation", evidence.ConfidenceMedium, rel)
		if s.res.Meta.Description == "" {
			// Instruction-like text is never allowed to become a derived field.
			s.res.Meta.Description = safeDescription(text)
		}
		s.scanDocumented(text, rel)

	case RoleDocs:
		s.add("documentation", evidence.ConfidenceMedium, rel)
		s.scanDocumented(text, rel)

	case RoleManifest:
		// Manifest contents are parsed in a second pass so that project
		// identity can be decided by root-level manifests only.
		s.scanShell(text, rel)

	case RoleDocker:
		s.add("docker_image", evidence.ConfidenceHigh, rel+" defines a container build")
		if strings.HasPrefix(lower, "docker-compose") || lower == "compose.yml" || lower == "compose.yaml" {
			s.add("docker_compose", evidence.ConfidenceHigh, rel+" defines compose services")
		}
		if sudoRe.MatchString(text) {
			s.security.Privileged = appendUnique(s.security.Privileged, rel)
			s.add("privileged_command", evidence.ConfidenceMedium, rel+" requests elevated privileges")
		}

	case RoleOpenAPI:
		s.add("openapi_spec", evidence.ConfidenceHigh, rel+" is an OpenAPI/Swagger document")
		s.add("documentation", evidence.ConfidenceLow, rel+" documents the HTTP surface")

	case RoleMCP:
		s.add("mcp_server_config", evidence.ConfidenceHigh, rel+" configures an MCP server")

	case RoleExecutable:
		if hasShebang(text) {
			s.add("cli_entrypoint", evidence.ConfidenceHigh, rel+" is an executable script with a shebang")
			// An executable is exactly where runnable commands live.
			s.scanShell(text, rel)
		}

	case RoleTests:
		s.add("has_tests", evidence.ConfidenceHigh, rel+" is a test file")

	case RoleWorkflow:
		s.add("has_github_actions", evidence.ConfidenceHigh, rel+" is a CI workflow")

	case RoleLicense:
		s.add("license_present", evidence.ConfidenceHigh, rel+" is a license file")

	case RoleEnvExample:
		s.add("env_example_present", evidence.ConfidenceHigh, rel+" is an environment template")

	case RoleMakefile:
		s.add("build_tooling", evidence.ConfidenceLow, rel+" defines build tasks")
		s.scanShell(text, rel)

	case RoleOther:
		if secretRe.MatchString(rel) {
			s.security.SecretLikeFiles = appendUnique(s.security.SecretLikeFiles, rel)
			s.add("secret_like_file", evidence.ConfidenceMedium,
				rel+" looks like a secret-bearing file; its contents were not imported")
		}
		if hasShebang(text) {
			s.add("cli_entrypoint", evidence.ConfidenceHigh, rel+" is an executable script with a shebang")
		}
		if serviceRe.MatchString(text) && isServiceName(rel) {
			s.add("local_service_hint", evidence.ConfidenceMedium, rel+" looks like a long-running local service")
		}
		// Only configuration files can declare an MCP server. A source file
		// that merely mentions mcpServers is not a configuration.
		if isConfigFile(rel) && looksLikeMCP(text) {
			s.add("mcp_server_config", evidence.ConfidenceMedium, rel+" declares MCP server configuration")
		}
		// Shell-like files are the only place a command can actually be run.
		if isShellLike(rel, text) {
			s.scanShell(text, rel)
		}
	}
}

// isConfigFile reports whether a file is structured configuration.
func isConfigFile(rel string) bool {
	switch strings.ToLower(path.Ext(rel)) {
	case ".json", ".yaml", ".yml", ".toml", ".ini", ".cfg":
		return true
	}
	base := strings.ToLower(path.Base(rel))
	return strings.HasPrefix(base, ".mcp") || base == "opencode.json"
}

// isShellLike reports whether a file can plausibly contain runnable commands.
func isShellLike(rel string, text string) bool {
	switch strings.ToLower(path.Ext(rel)) {
	case ".sh", ".bash", ".zsh", ".fish", ".ps1", ".bat", ".cmd", ".mk":
		return true
	}
	switch path.Base(rel) {
	case "Makefile", "makefile", "GNUmakefile", "Dockerfile", "Containerfile":
		return true
	}
	return strings.HasPrefix(text, "#!")
}

func isServiceName(rel string) bool {
	lower := strings.ToLower(path.Base(rel))
	return lower == "server.js" || lower == "server.ts" || lower == "app.py" ||
		lower == "main.py" || lower == "server.py" || strings.HasPrefix(lower, "server")
}

func looksLikeMCP(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "mcpservers") ||
		(strings.Contains(lower, "modelcontextprotocol") && strings.Contains(lower, "server"))
}

func hasShebang(text string) bool { return strings.HasPrefix(text, "#!") }

// safeDescription returns prose that is safe to carry forward as metadata,
// skipping any line that reads like an instruction to an agent.
func safeDescription(text string) string {
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if injectionRe.MatchString(t) {
			continue
		}
		return truncate(t, 280)
	}
	return ""
}

func (s *scanner) scanShell(text, rel string) {
	if downloadRe.MatchString(text) {
		s.security.NetworkDownloads = appendUnique(s.security.NetworkDownloads, rel)
		s.add("network_download_script", evidence.ConfidenceMedium,
			rel+" contains a network download command")
	}
	if sudoRe.MatchString(text) {
		s.security.Privileged = appendUnique(s.security.Privileged, rel)
		s.add("privileged_command", evidence.ConfidenceMedium, rel+" requests elevated privileges")
	}
}

// scanDocumented reports commands that prose merely *describes*. The wording
// differs from scanShell on purpose: a README that mentions sudo is documenting
// it, not demanding it.
func (s *scanner) scanDocumented(text, rel string) {
	if downloadRe.MatchString(text) {
		s.security.NetworkDownloads = appendUnique(s.security.NetworkDownloads, rel)
		s.add("documented_download_command", evidence.ConfidenceLow,
			rel+" documents a network download command")
	}
	if sudoRe.MatchString(text) {
		s.security.Privileged = appendUnique(s.security.Privileged, rel)
		s.add("documented_privileged_command", evidence.ConfidenceLow,
			rel+" documents an elevated-privilege command")
	}
}

func appendUnique(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

var lifecycleScripts = []string{"preinstall", "install", "postinstall", "prepare", "prepublish", "prepublishOnly"}

func (s *scanner) recordInstallScripts(scripts map[string]string, rel string) {
	for _, name := range lifecycleScripts {
		body, ok := scripts[name]
		if !ok || strings.TrimSpace(body) == "" {
			continue
		}
		s.security.InstallScripts = appendUnique(s.security.InstallScripts, rel+"#"+name)
		s.add("install_script_present", evidence.ConfidenceHigh,
			fmt.Sprintf("%s defines %s: %s (recorded only; never executed during inspection)", rel, name, truncate(body, 120)))
	}
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}

func (m *Meta) finalize() {
	m.Languages = sortedUnique(m.Languages)
	m.Entrypoints = sortedUnique(m.Entrypoints)
	m.EntrypointPaths = sortedUnique(m.EntrypointPaths)
	m.Dependencies = sortedUnique(m.Dependencies)
}

func sortedUnique(list []string) []string {
	if len(list) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(list))
	for _, v := range list {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
