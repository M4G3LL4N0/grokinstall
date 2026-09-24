package redact

import (
	"strings"
	"testing"
)

func TestDetectsSecretPaths(t *testing.T) {
	secretPaths := []string{
		".env", "config/.env.production", "~/.netrc", "id_rsa", "server.pem",
		"certs/key.p12", "app/credentials.json", "deploy/secrets.yaml",
		".npmrc", "keystore.jks",
	}
	for _, p := range secretPaths {
		if !IsSecretPath(p) {
			t.Fatalf("%q should be treated as secret-bearing", p)
		}
	}
}

func TestTemplatesAreNotSecrets(t *testing.T) {
	for _, p := range []string{".env.example", ".env.sample", "config.template.json", "defaults.yaml"} {
		if IsSecretPath(p) {
			t.Fatalf("%q is a template, not a secret", p)
		}
	}
}

func TestRedactsTokenAssignment(t *testing.T) {
	in := "export API_TOKEN=abc123supersecret and GITHUB_TOKEN=zzz999"
	out := String(in)
	if strings.Contains(out, "abc123supersecret") || strings.Contains(out, "zzz999") {
		t.Fatalf("secret values survived redaction: %q", out)
	}
	if !strings.Contains(out, "API_TOKEN") {
		t.Fatalf("the variable name should survive so the shape is preserved: %q", out)
	}
}

func TestRedactsKnownTokenShapes(t *testing.T) {
	cases := map[string]string{
		"github":   "token ghp_abcdefghijklmnopqrstuvwxyz012345",
		"openai":   "key sk-abcdefghijklmnopqrstuvwxyz123456",
		"aws":      "AKIAIOSFODNN7EXAMPLE",
		"slack":    "xoxb-123456789012-abcdefghijkl",
		"jwt":      "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dBjftJeZ4CVPmB92K27uhbUJU1p1r_wW1gFWFOEjXk",
		"pem":      "-----BEGIN RSA PRIVATE KEY-----\nMIIEpAIBAAKCAQEA\n-----END RSA PRIVATE KEY-----",
		"password": "PASSWORD=hunter2hunter2",
	}
	for name, in := range cases {
		out := String(in)
		if ContainsSecret(out) {
			t.Fatalf("%s: secret survived redaction: %q", name, out)
		}
	}
}

func TestLeavesOrdinaryTextAlone(t *testing.T) {
	in := "Run the widget command to search the repository for a term."
	if String(in) != in {
		t.Fatalf("ordinary text was modified: %q", String(in))
	}
	if ContainsSecret(in) {
		t.Fatal("ordinary text should not be flagged")
	}
}

func TestLeavesCodeExamplesAlone(t *testing.T) {
	// Documentation that merely *names* a variable must not be redacted into
	// uselessness, and must not be flagged as a secret either.
	in := "Set GITHUB_TOKEN in your environment before running the tool."
	if ContainsSecret(in) {
		t.Fatalf("a variable name without a value is not a secret: %q", in)
	}
}

func TestRedactAllReportsChange(t *testing.T) {
	values := []string{"clean", "TOKEN=abc123xyz"}
	out, changed := RedactAll(values)
	if !changed {
		t.Fatal("change should be reported")
	}
	if strings.Contains(out[1], "abc123xyz") {
		t.Fatal("secret survived")
	}
	if out[0] != "clean" {
		t.Fatal("clean value was modified")
	}
}

func TestRedactAllEmptyInput(t *testing.T) {
	out, changed := RedactAll(nil)
	if changed || len(out) != 0 {
		t.Fatal("empty input should be a no-op")
	}
}
