package adapter

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// pathOf returns the directory containing a command, for a minimal probe PATH.
func pathOf(command string) string { return filepath.Dir(command) }

// probeHome is deliberately empty so a probe cannot read the user's shell or
// tool configuration.
const probeHome = ""

// emptyReader is attached to probe stdin so a help invocation can never block.
var emptyReader = strings.NewReader("")

// baseName returns the executable's base name, lowercased.
func baseName(command string) string {
	if command == "" {
		return ""
	}
	return strings.ToLower(filepath.Base(command))
}

// longFlagRe finds long flags as whole tokens. It is intentionally strict: a
// flag only counts when it appears as a token, so ordinary prose in help text
// cannot manufacture one.
var longFlagRe = regexp.MustCompile(`(^|\s)(--[a-z0-9][a-z0-9-]*)`)

// longFlagTokens extracts the long flags in one line of help text.
func longFlagTokens(line string) []string {
	matches := longFlagRe.FindAllStringSubmatch(line, -1)
	var out []string
	for _, m := range matches {
		if len(m) > 1 {
			out = append(out, m[1])
		}
	}
	return out
}

// captureBounded runs a command and reads at most limit bytes of stdout. It
// reports whether the bound was reached. The child's output is drained so a
// chatty process never blocks, and can always be terminated.
func captureBounded(cmd *exec.Cmd, limit int) (string, bool, error) {
	sink := &cappedWriter{limit: limit}
	cmd.Stdout = sink
	err := cmd.Run()
	return sink.buf.String(), sink.exceeded, err
}

// cappedWriter buffers up to limit bytes and discards the rest while still
// reporting success.
type cappedWriter struct {
	buf      bytes.Buffer
	limit    int
	exceeded bool
}

func (c *cappedWriter) Write(p []byte) (int, error) {
	if c.buf.Len() < c.limit {
		remaining := c.limit - c.buf.Len()
		if len(p) <= remaining {
			return c.buf.Write(p)
		}
		c.buf.Write(p[:remaining])
		c.exceeded = true
		return len(p), nil
	}
	c.exceeded = true
	return len(p), nil
}
