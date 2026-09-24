package toolchain

// NeedLabel states how a plan relates to a tool.
type NeedLabel string

// Need labels. They describe the relationship between a plan and a tool, not
// a global ranking: a tool can be optional, irrelevant, missing, or satisfied
// by a substitute.
const (
	LabelRequired            NeedLabel = "required"
	LabelRecommended         NeedLabel = "recommended"
	LabelOptional            NeedLabel = "optional"
	LabelIrrelevant          NeedLabel = "irrelevant"
	LabelMissing             NeedLabel = "missing"
	LabelSubstituteAvailable NeedLabel = "substitute_available"
)

// NeedSet groups tools by the role they play in a plan.
type NeedSet struct {
	Required    []ToolID
	Recommended []ToolID
	Optional    []ToolID
	Irrelevant  []ToolID
}

// Need is one tool resolved against a plan.
type Need struct {
	Tool     ToolID    `json:"tool"`
	Name     string    `json:"name"`
	Label    NeedLabel `json:"label"`
	Required bool      `json:"required"`
	Resolved ToolID    `json:"resolved,omitempty"`
	Version  string    `json:"version,omitempty"`
	Why      string    `json:"why,omitempty"`
}

// Classify resolves a plan's needs against this machine. A tool that is
// required but absent is reported as missing while still being marked required,
// so a plan can never quietly downgrade a hard requirement.
func (p *Profile) Classify(set NeedSet) []Need {
	var out []Need
	seen := map[ToolID]bool{}

	appendBucket := func(ids []ToolID, label NeedLabel, required bool) {
		for _, id := range ids {
			if seen[id] {
				continue
			}
			seen[id] = true
			tool := p.Get(id)
			why := tool.Why
			if why == "" {
				why = toolFor(id).why
			}
			need := Need{Tool: id, Name: tool.Name, Required: required, Why: why}
			if tool.Present {
				need.Resolved = id
				need.Version = tool.Version
			}
			// Irrelevant is a statement about the plan, not the machine: it is
			// never downgraded to "missing" just because the tool is absent.
			switch {
			case label == LabelIrrelevant:
				need.Label = LabelIrrelevant
			case tool.Present:
				need.Label = label
			default:
				if res, ok := p.Resolve(id); ok && res.Status == StatusSubstitute {
					sub := p.Get(res.Resolved)
					need.Resolved = res.Resolved
					need.Version = sub.Version
					need.Label = LabelSubstituteAvailable
					need.Why = why + "; satisfied by " + sub.Name
				} else {
					need.Label = LabelMissing
				}
			}
			out = append(out, need)
		}
	}

	appendBucket(set.Required, LabelRequired, true)
	appendBucket(set.Recommended, LabelRecommended, false)
	appendBucket(set.Optional, LabelOptional, false)
	appendBucket(set.Irrelevant, LabelIrrelevant, false)
	return out
}

// SubstituteFor returns a present substitute for a tool, if any.
func (p *Profile) SubstituteFor(id ToolID) (ToolID, bool) {
	res, ok := p.Resolve(id)
	if ok && res.Status == StatusSubstitute {
		return res.Resolved, true
	}
	return "", false
}

// AnyCodingTool returns the first installed coding agent, preferring OpenCode.
func (p *Profile) AnyCodingTool() (ToolID, bool) {
	for _, id := range []ToolID{ToolOpenCode, ToolCursor} {
		if p.Get(id).Present {
			return id, true
		}
	}
	for _, id := range []ToolID{ToolOpenCode, ToolCursor} {
		if sub, ok := p.SubstituteFor(id); ok {
			return sub, true
		}
	}
	return "", false
}
