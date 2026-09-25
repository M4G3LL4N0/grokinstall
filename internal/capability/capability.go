// Package capability turns evidence into the capabilities a source can expose
// to GrokBot. Discovery is deterministic: a capability exists only when an
// inspected artifact backs it.
package capability

import (
	"fmt"
	"sort"

	"github.com/M4G3LL4N0/grokinstall/internal/evidence"
)

// Kind is the kind of surface a capability is reached through.
type Kind string

// Capability kinds.
const (
	KindCLI          Kind = "cli"
	KindAPI          Kind = "api"
	KindMCP          Kind = "mcp"
	KindDocker       Kind = "docker"
	KindKnowledge    Kind = "knowledge"
	KindLocalService Kind = "local_service"
)

// Capability is one reachable capability plus the evidence that found it.
type Capability struct {
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Kind        Kind                `json:"kind"`
	Entrypoints []string            `json:"entrypoints,omitempty"`
	Confidence  evidence.Confidence `json:"confidence"`
	Evidence    []string            `json:"evidence"`
	Usable      bool                `json:"usable"`
	Status      string              `json:"status"`
}

var findingToCapability = []struct {
	finding string
	kind    Kind
	usable  bool
}{
	{"cli_entrypoint", KindCLI, true},
	{"openapi_spec", KindAPI, false},
	{"mcp_server_config", KindMCP, false},
	{"docker_image", KindDocker, false},
	{"local_service_hint", KindLocalService, false},
	{"documentation", KindKnowledge, true},
}

// Discover builds capabilities from inspection evidence.
func Discover(ev *evidence.Set) []Capability {
	if ev == nil {
		return nil
	}
	var out []Capability
	for _, m := range findingToCapability {
		if !ev.Has(m.finding) {
			continue
		}
		item := ev.Get(m.finding)
		status := "detected"
		if m.usable {
			status = "usable"
		}
		out = append(out, Capability{
			Name:        string(m.kind) + ".main",
			Description: describe(m.kind),
			Kind:        m.kind,
			Confidence:  item.Confidence,
			Evidence:    item.Evidence,
			Usable:      m.usable,
			Status:      status,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return out
}

func describe(k Kind) string {
	switch k {
	case KindCLI:
		return "invoke the project's declared command line entrypoint"
	case KindAPI:
		return "call the declared HTTP API surface"
	case KindMCP:
		return "call the declared MCP server"
	case KindDocker:
		return "run the declared container image"
	case KindKnowledge:
		return "search the project's own documentation"
	case KindLocalService:
		return "call the project's local service"
	default:
		return fmt.Sprintf("use the source as %s", k)
	}
}

// Find returns the first capability of a kind.
func Find(caps []Capability, kind Kind) (Capability, bool) {
	for _, c := range caps {
		if c.Kind == kind {
			return c, true
		}
	}
	return Capability{}, false
}

// HasKind reports whether any capability of a kind exists.
func HasKind(caps []Capability, kind Kind) bool {
	_, ok := Find(caps, kind)
	return ok
}
