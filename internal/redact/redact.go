package redact

import (
	"regexp"
	"strings"
)

// Placeholder replaces redacted material so a reader knows something was
// removed without learning its value.
const Placeholder = "[redacted]"

// Assignment of a secret-looking value. RE2 has no backreferences, so the
// optional quotes are matched as part of the value rather than referenced.
var assignmentRe = regexp.MustCompile(`(?i)\b([A-Z0-9_]*(?:TOKEN|SECRET|PASSWORD|PASSWD|API_?KEY|ACCESS_?KEY|PRIVATE_?KEY|CREDENTIAL)[A-Z0-9_]*)\s*[:=]\s*["']?[^\s"']+["']?`)

// Known private key headers.
var keyHeaderRe = regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)

// Provider token shapes that are recognizable without being exhaustive.
var tokenShapes = []struct {
	name string
	re   *regexp.Regexp
}{
	{"github token", regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{16,}\b`)},
	{"openai key", regexp.MustCompile(`\bsk-[A-Za-z0-9]{20,}\b`)},
	{"aws access key", regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)},
	{"slack token", regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}\b`)},
	{"jwt", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`)},
}

// IsSecretPath reports whether a file path is likely to hold secrets.
func IsSecretPath(path string) bool {
	lower := strings.ToLower(path)
	base := lower
	if idx := strings.LastIndex(lower, "/"); idx >= 0 {
		base = lower[idx+1:]
	}
	// Templates and examples are checked first: they describe the shape of a
	// secret without holding one.
	if isTemplateName(base) {
		return false
	}
	switch {
	case base == ".env" || strings.HasPrefix(base, ".env."):
		return true
	case base == ".netrc", base == ".npmrc", base == ".pypirc", base == ".pgpass":
		return true
	case base == "id_rsa", base == "id_ed25519", base == "id_ecdsa", base == "id_dsa":
		return true
	case strings.HasSuffix(base, ".pem"), strings.HasSuffix(base, ".p12"),
		strings.HasSuffix(base, ".pfx"), strings.HasSuffix(base, ".key"):
		return true
	case strings.HasSuffix(base, ".keystore"), strings.HasSuffix(base, ".jks"):
		return true
	case strings.Contains(base, "credential"), strings.Contains(base, "secret"):
		return true
	case base == "secrets.json", base == "secrets.yaml", base == "secrets.yml":
		return true
	}
	return false
}

func isTemplateName(base string) bool {
	for _, marker := range []string{"example", "sample", "template", "dist", "default", ".placeholder"} {
		if strings.Contains(base, marker) {
			return true
		}
	}
	return false
}

// Findings reports what kind of secret material a text contains, without
// reproducing it. Already-redacted text reports nothing, so redaction is
// idempotent and a sanitized string can be re-inspected safely.
func Findings(text string) []string {
	scrubbed := strings.ReplaceAll(text, Placeholder, "")
	var out []string
	if keyHeaderRe.MatchString(scrubbed) {
		out = append(out, "private key material")
	}
	if assignmentRe.MatchString(scrubbed) {
		out = append(out, "secret-shaped assignment")
	}
	for _, shape := range tokenShapes {
		if shape.re.MatchString(scrubbed) {
			out = append(out, shape.name)
		}
	}
	return out
}

// ContainsSecret reports whether text looks like it holds secret material.
func ContainsSecret(text string) bool { return len(Findings(text)) > 0 }

// String removes secret-looking values while preserving the surrounding shape of
// the text, so a document can still be summarized honestly.
func String(text string) string {
	if text == "" {
		return text
	}
	out := keyHeaderRe.ReplaceAllString(text, Placeholder)
	out = assignmentRe.ReplaceAllString(out, "${1}="+Placeholder)
	for _, shape := range tokenShapes {
		out = shape.re.ReplaceAllString(out, Placeholder)
	}
	return out
}

// RedactAll applies redaction across a list of strings, reporting whether
// anything was removed.
func RedactAll(values []string) ([]string, bool) {
	changed := false
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = String(v)
		if out[i] != v {
			changed = true
		}
	}
	return out, changed
}
