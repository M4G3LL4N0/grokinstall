package installer

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/M4G3LL4N0/grokinstall/internal/manifest"
	"github.com/M4G3LL4N0/grokinstall/internal/plan"
	"github.com/M4G3LL4N0/grokinstall/internal/receipt"
	"github.com/M4G3LL4N0/grokinstall/internal/strategy"
)

func newInstallID() string {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("gi_%d", time.Now().UnixNano())
	}
	return "gi_" + hex.EncodeToString(buf)
}

// chooseName derives a stable capability name from the source and goal.
func chooseName(opts Options, built *plan.Plan) string {
	if strings.TrimSpace(opts.Name) != "" {
		return sanitize(opts.Name)
	}
	base := built.Manifest.Name
	if base == "" || base == "source" {
		base = "capability"
	}
	verb := goalVerb(built.Goal)
	if verb == "" {
		verb = defaultVerb(built.Comparison.Recommended)
	}
	return sanitize(base + "." + verb)
}

func defaultVerb(id strategy.ID) string {
	switch id {
	case strategy.StrategyKnowledgeImport:
		return "search"
	case strategy.StrategyAPIBridge:
		return "api"
	case strategy.StrategyMCPBridge:
		return "mcp"
	case strategy.StrategyDockerBridge:
		return "run"
	case strategy.StrategyLocalService:
		return "call"
	default:
		return "cli"
	}
}

func goalVerb(goal string) string {
	lower := strings.ToLower(goal)
	table := []struct{ verb, keywords string }{
		{"search", "search find query lookup look up"},
		{"docs", "documentation docs readme guide manual reference"},
		{"security", "security audit vulnerability"},
		{"diagnose", "diagnose debug troubleshoot"},
		{"review", "review inspect analyse analyze check"},
		{"convert", "convert transform format"},
		{"api", "api endpoint rest http"},
		{"run", "run execute invoke start"},
	}
	for _, entry := range table {
		for _, kw := range strings.Fields(entry.keywords) {
			if strings.Contains(lower, kw) {
				return entry.verb
			}
		}
	}
	return ""
}

func sanitize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-._")
	if out == "" {
		return "capability"
	}
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}

func failedChecks(v receipt.Verification) []string {
	var out []string
	for _, c := range v.Checks {
		if !c.Passed {
			out = append(out, c.Name+": "+c.Detail)
		}
	}
	return out
}

func verificationCommands(opts Options, m *manifest.Manifest) []receipt.Command {
	if m == nil || m.Execution.Type != "subprocess" {
		return []receipt.Command{}
	}
	return []receipt.Command{{
		Binary: m.Execution.Command,
		Args:   m.Execution.Args,
		Reason: "capability smoke test",
	}}
}

// finalFileChanges rewrites staged paths to their committed locations so the
// receipt describes state a user can actually inspect.
func finalFileChanges(created []receipt.FileChange, stageDir, name string, committed commitResult) []receipt.FileChange {
	out := make([]receipt.FileChange, 0, len(created))
	for _, c := range created {
		rel := strings.TrimPrefix(c.Path, stageDir+string(filepath.Separator))
		newPath := committed.manifestPath
		if rel != name+".json" {
			if committed.adapterPath == "" {
				continue
			}
			rest := strings.TrimPrefix(rel, name+string(filepath.Separator))
			newPath = filepath.Join(committed.adapterPath, rest)
		}
		out = append(out, changeFor(newPath, c.Action, c.Owner))
	}
	return out
}
