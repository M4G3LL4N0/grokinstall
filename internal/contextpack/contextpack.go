// Package contextpack defines the compact, bounded payload that may be handed
// to a reasoning or coding worker. It exists to keep whole repositories out of
// model context: a pack carries selected metadata, evidence, capabilities,
// questions and constraints, never raw source files.
package contextpack

import (
	"encoding/json"

	"github.com/M4G3LL4N0/grokinstall/internal/capability"
	"github.com/M4G3LL4N0/grokinstall/internal/evidence"
	"github.com/M4G3LL4N0/grokinstall/internal/redact"
	"github.com/M4G3LL4N0/grokinstall/internal/source"
)

// Schema identifies the context pack format.
const Schema = "grokinstall/contextpack/v1"

// MaxMetadataValueBytes bounds any single metadata string.
const MaxMetadataValueBytes = 512

// Pack is the only structure workers are handed by GrokInstall.
type Pack struct {
	Schema           string                  `json:"schema"`
	Goal             string                  `json:"goal"`
	Source           *source.Source          `json:"source,omitempty"`
	SourceMetadata   map[string]string       `json:"source_metadata,omitempty"`
	ManifestEvidence []evidence.Item         `json:"manifest_evidence,omitempty"`
	Capabilities     []capability.Capability `json:"capabilities,omitempty"`
	Unresolved       []string                `json:"unresolved_questions,omitempty"`
	Constraints      []string                `json:"constraints,omitempty"`
}

// New returns an empty pack with the schema set.
func New() *Pack {
	return &Pack{Schema: Schema}
}

// WithGoal sets the goal.
func (p *Pack) WithGoal(goal string) *Pack {
	p.Goal = goal
	return p
}

// WithSource records the normalized source identity.
func (p *Pack) WithSource(src source.Source) *Pack {
	s := src
	p.Source = &s
	return p
}

// WithMetadata adds one bounded metadata entry.
func (p *Pack) WithMetadata(key, value string) *Pack {
	if p.SourceMetadata == nil {
		p.SourceMetadata = map[string]string{}
	}
	// Redact first so the shape of the text survives: a reader still sees
	// which variable was involved, never its value.
	value = redact.String(value)
	if len(value) > MaxMetadataValueBytes {
		value = value[:MaxMetadataValueBytes]
	}
	p.SourceMetadata[key] = value
	return p
}

// WithEvidence adds inspection evidence. Evidence text is redacted before it
// enters the pack, so a repository that prints a token in a diagnostic string
// cannot smuggle it into a model context.
func (p *Pack) WithEvidence(items []evidence.Item) *Pack {
	safe := make([]evidence.Item, 0, len(items))
	for _, it := range items {
		it.Evidence, _ = redact.RedactAll(it.Evidence)
		safe = append(safe, it)
	}
	p.ManifestEvidence = safe
	return p
}

// WithSourceMetadataFiltered adds metadata only when it is safe to carry.
func (p *Pack) WithSourceMetadataFiltered(key, value string) *Pack {
	if redact.ContainsSecret(value) {
		return p.WithMetadata(key, "[redacted: source metadata contains secret material]")
	}
	return p.WithMetadata(key, value)
}

// WithCapabilities adds discovered capabilities.
func (p *Pack) WithCapabilities(caps []capability.Capability) *Pack {
	p.Capabilities = caps
	return p
}

// WithUnresolved adds unresolved questions.
func (p *Pack) WithUnresolved(q []string) *Pack {
	p.Unresolved = q
	return p
}

// WithConstraints adds explicit constraints.
func (p *Pack) WithConstraints(c []string) *Pack {
	p.Constraints = c
	return p
}

// JSON serializes the pack.
func (p *Pack) JSON() ([]byte, error) { return json.Marshal(p) }

// MarshalJSON enforces the pack's bounds at serialization time, so a pack
// cannot carry unbounded metadata or oversized sections even if it was built
// without the fluent helpers.
func (p Pack) MarshalJSON() ([]byte, error) {
	type alias Pack
	out := alias(p)
	if out.Schema == "" {
		out.Schema = Schema
	}
	if out.SourceMetadata != nil {
		bounded := make(map[string]string, len(out.SourceMetadata))
		for k, v := range out.SourceMetadata {
			v = redact.String(v)
			if len(v) > MaxMetadataValueBytes {
				v = v[:MaxMetadataValueBytes]
			}
			bounded[k] = v
		}
		out.SourceMetadata = bounded
	}
	if out.Constraints != nil {
		clean := make([]string, len(out.Constraints))
		clean, _ = redact.RedactAll(out.Constraints)
		out.Constraints = clean
	}
	return json.Marshal(out)
}

// SizeBytes is the serialized size of the pack, used to keep context small and
// measurable without claiming token savings.
func (p *Pack) SizeBytes() int {
	data, err := p.JSON()
	if err != nil {
		return 0
	}
	return len(data)
}
