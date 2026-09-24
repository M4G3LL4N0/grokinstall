// Package strategy compares integration approaches on evidence and toolchain
// facts. Ratings are qualitative and always explain themselves: no fabricated
// savings, latency numbers or cost estimates.
package strategy

import (
	"fmt"
	"sort"
	"strings"

	"grokinstall/internal/capability"
	"grokinstall/internal/evidence"
	"grokinstall/internal/toolchain"
)

// ID identifies an integration strategy.
type ID string

// The strategies GrokInstall can consider.
const (
	StrategyCLIBridge         ID = "cli_bridge"
	StrategyAPIBridge         ID = "api_bridge"
	StrategyMCPBridge         ID = "mcp_bridge"
	StrategyDockerBridge      ID = "docker_bridge"
	StrategyLocalService      ID = "local_service"
	StrategyKnowledgeImport   ID = "knowledge_import"
	StrategyMicroPromptPack   ID = "micro_prompt_pack"
	StrategyGeneratedAdapter  ID = "generated_adapter"
	StrategyExternalExecution ID = "external_execution"
	StrategyNoInstall         ID = "no_install"
)

// All is the stable comparison order.
var All = []ID{
	StrategyCLIBridge, StrategyAPIBridge, StrategyMCPBridge, StrategyDockerBridge,
	StrategyLocalService, StrategyKnowledgeImport, StrategyMicroPromptPack,
	StrategyGeneratedAdapter, StrategyExternalExecution, StrategyNoInstall,
}

// Support states how much of a strategy Part 1 actually implements.
type Support string

// Support levels.
const (
	SupportFull        Support = "full"
	SupportPlanned     Support = "planned"
	SupportUnsupported Support = "unsupported"
)

// Dimension is one axis of comparison.
type Dimension string

// The comparison dimensions.
const (
	DimFootprint    Dimension = "grokbot_footprint"
	DimCoverage     Dimension = "feature_coverage"
	DimLocal        Dimension = "local_execution"
	DimExternalCost Dimension = "external_cost"
	DimLatency      Dimension = "latency"
	DimMaintenance  Dimension = "maintenance"
	DimSecurity     Dimension = "security"
	DimPrivacy      Dimension = "privacy"
	DimCacheability Dimension = "cacheability"
	DimDependencies Dimension = "dependencies"
	DimEffort       Dimension = "implementation_effort"
	DimPortability  Dimension = "portability"
)

// Dimensions is the stable order used in output.
var Dimensions = []Dimension{
	DimFootprint, DimCoverage, DimLocal, DimExternalCost, DimLatency, DimMaintenance,
	DimSecurity, DimPrivacy, DimCacheability, DimDependencies, DimEffort, DimPortability,
}

// Rating is a qualitative verdict, never a fabricated measurement.
type Rating string

// Rating values.
const (
	RatingExcellent Rating = "excellent"
	RatingGood      Rating = "good"
	RatingFair      Rating = "fair"
	RatingPoor      Rating = "poor"
	RatingBlocked   Rating = "blocked"
	RatingNA        Rating = "not_applicable"
)

// Assessment is one dimension verdict with its reason.
type Assessment struct {
	Dimension Dimension `json:"dimension"`
	Rating    Rating    `json:"rating"`
	Reason    string    `json:"reason"`
}

// Candidate is one strategy considered for a source.
type Candidate struct {
	Strategy    ID                                    `json:"strategy"`
	Applicable  bool                                  `json:"applicable"`
	Feasibility Rating                                `json:"feasibility"`
	Support     Support                               `json:"support"`
	Assessments []Assessment                          `json:"assessments"`
	Evidence    []string                              `json:"evidence"`
	Requires    []toolchain.ToolID                    `json:"requires_tools,omitempty"`
	Substitutes map[toolchain.ToolID]toolchain.ToolID `json:"substitutes,omitempty"`
	Reason      string                                `json:"reason"`
}

// Comparison is the full side-by-side result.
type Comparison struct {
	Goal                   string              `json:"goal"`
	Candidates             []Candidate         `json:"candidates"`
	Recommended            ID                  `json:"recommended"`
	RecommendationReason   string              `json:"recommendation_reason"`
	RecommendationEvidence []string            `json:"recommendation_evidence"`
	Confidence             evidence.Confidence `json:"confidence"`
}

// Candidate returns a candidate by strategy, or nil.
func (c *Comparison) Candidate(id ID) *Candidate {
	for i := range c.Candidates {
		if c.Candidates[i].Strategy == id {
			return &c.Candidates[i]
		}
	}
	return nil
}

type strategySpec struct {
	id       ID
	support  Support
	capKind  capability.Kind
	assess   func(ctx compareContext) []Assessment
	feasible func(ctx compareContext) (bool, Rating, string, []toolchain.ToolID, map[toolchain.ToolID]toolchain.ToolID)
}

type compareContext struct {
	goal    string
	caps    []capability.Capability
	ev      *evidence.Set
	profile *toolchain.Profile
}

func (ctx compareContext) has(kind capability.Kind) bool {
	return capability.HasKind(ctx.caps, kind)
}

func (ctx compareContext) evidenceFor(finding string) []string {
	if ctx.ev == nil || !ctx.ev.Has(finding) {
		return nil
	}
	return ctx.ev.Get(finding).Evidence
}

func hasTool(p *toolchain.Profile, id toolchain.ToolID) bool {
	return p != nil && p.Get(id).Present
}

func substitutions(p *toolchain.Profile, ids []toolchain.ToolID) map[toolchain.ToolID]toolchain.ToolID {
	var out map[toolchain.ToolID]toolchain.ToolID
	if p == nil {
		return nil
	}
	for _, id := range ids {
		if sub, ok := p.SubstituteFor(id); ok {
			if out == nil {
				out = map[toolchain.ToolID]toolchain.ToolID{}
			}
			out[id] = sub
		}
	}
	return out
}

func assess(base map[Dimension]Assessment, order []Dimension) []Assessment {
	out := make([]Assessment, 0, len(order))
	for _, d := range order {
		a, ok := base[d]
		if !ok {
			a = Assessment{Dimension: d, Rating: RatingNA, Reason: "not relevant to this strategy"}
		}
		a.Dimension = d
		out = append(out, a)
	}
	return out
}

// Compare builds a full comparison of every strategy for a source and goal.
func Compare(goal string, capsList []capability.Capability, ev *evidence.Set, profile *toolchain.Profile) *Comparison {
	ctx := compareContext{goal: strings.ToLower(goal), caps: capsList, ev: ev, profile: profile}
	if ctx.profile == nil {
		ctx.profile = &toolchain.Profile{Tools: map[toolchain.ToolID]toolchain.Tool{}}
	}

	out := &Comparison{Goal: goal, Confidence: evidence.ConfidenceMedium}
	for _, spec := range specs() {
		cand := buildCandidate(spec, ctx)
		out.Candidates = append(out.Candidates, cand)
	}
	out.Recommended, out.RecommendationReason, out.RecommendationEvidence, out.Confidence = recommend(ctx)
	return out
}

func buildCandidate(spec strategySpec, ctx compareContext) Candidate {
	applicable, feasibility, reason, requires, _ := spec.feasible(ctx)
	cand := Candidate{
		Strategy:    spec.id,
		Applicable:  applicable,
		Feasibility: feasibility,
		Support:     spec.support,
		Reason:      reason,
		Requires:    requires,
		Substitutes: substitutions(ctx.profile, requires),
	}
	if applicable {
		cand.Assessments = spec.assess(ctx)
	} else {
		base := map[Dimension]Assessment{}
		for _, d := range Dimensions {
			base[d] = Assessment{Dimension: d, Rating: RatingNA, Reason: "not applicable: " + reason}
		}
		cand.Assessments = assess(base, Dimensions)
	}
	for _, kind := range []capability.Kind{capability.KindCLI, capability.KindAPI, capability.KindMCP, capability.KindDocker, capability.KindLocalService, capability.KindKnowledge} {
		if ctx.has(kind) {
			cand.Evidence = append(cand.Evidence, ctx.evidenceFor(findingFor(kind))...)
		}
	}
	sort.Strings(cand.Evidence)
	return cand
}

func findingFor(kind capability.Kind) string {
	switch kind {
	case capability.KindCLI:
		return "cli_entrypoint"
	case capability.KindAPI:
		return "openapi_spec"
	case capability.KindMCP:
		return "mcp_server_config"
	case capability.KindDocker:
		return "docker_image"
	case capability.KindLocalService:
		return "local_service_hint"
	case capability.KindKnowledge:
		return "documentation"
	}
	return ""
}

func specs() []strategySpec {
	return []strategySpec{
		{
			id:      StrategyCLIBridge,
			support: SupportFull,
			feasible: func(ctx compareContext) (bool, Rating, string, []toolchain.ToolID, map[toolchain.ToolID]toolchain.ToolID) {
				if !ctx.has(capability.KindCLI) {
					return false, RatingNA, "no CLI entrypoint was detected", nil, nil
				}
				ev := ctx.evidenceFor("cli_entrypoint")
				return true, RatingExcellent,
					fmt.Sprintf("a declared command can be wrapped as a subprocess capability (evidence: %s)", strings.Join(ev, "; ")),
					nil, nil
			},
			assess: func(ctx compareContext) []Assessment {
				return assess(map[Dimension]Assessment{
					DimFootprint:    {Rating: RatingExcellent, Reason: "GrokBot learns one compact manifest instead of reading the project"},
					DimCoverage:     {Rating: RatingFair, Reason: "only what the declared command exposes is reachable"},
					DimLocal:        {Rating: RatingExcellent, Reason: "runs as a local subprocess"},
					DimExternalCost: {Rating: RatingExcellent, Reason: "no provider calls in the execution path"},
					DimLatency:      {Rating: RatingGood, Reason: "one process spawn per call; deterministic work stays off the model path"},
					DimMaintenance:  {Rating: RatingGood, Reason: "upstream command owns its own updates"},
					DimSecurity:     {Rating: RatingFair, Reason: "executes the source command; scripts and permissions still need review"},
					DimPrivacy:      {Rating: RatingExcellent, Reason: "input and output stay on this machine"},
					DimCacheability: {Rating: RatingGood, Reason: "identical inputs can reuse a cached result"},
					DimDependencies: {Rating: RatingGood, Reason: "uses the declared runtime already present"},
					DimEffort:       {Rating: RatingGood, Reason: "a thin adapter plus a manifest"},
					DimPortability:  {Rating: RatingFair, Reason: "tied to the command and its runtime being installed"},
				}, Dimensions)
			},
		},
		{
			id:      StrategyAPIBridge,
			support: SupportPlanned,
			feasible: func(ctx compareContext) (bool, Rating, string, []toolchain.ToolID, map[toolchain.ToolID]toolchain.ToolID) {
				if !ctx.has(capability.KindAPI) {
					return false, RatingNA, "no OpenAPI or Swagger document was detected", nil, nil
				}
				return true, RatingGood,
					"an HTTP surface is declared, but Part 1 detects and compares it rather than installing it",
					nil, nil
			},
			assess: func(ctx compareContext) []Assessment {
				return assess(map[Dimension]Assessment{
					DimFootprint:    {Rating: RatingExcellent, Reason: "GrokBot holds an endpoint contract, not the service"},
					DimCoverage:     {Rating: RatingGood, Reason: "every documented operation is addressable"},
					DimLocal:        {Rating: RatingGood, Reason: "can point at a local service"},
					DimExternalCost: {Rating: RatingFair, Reason: "hosted APIs may charge per call"},
					DimLatency:      {Rating: RatingGood, Reason: "a single request, but network-bound"},
					DimMaintenance:  {Rating: RatingFair, Reason: "the remote service can change its schema"},
					DimSecurity:     {Rating: RatingFair, Reason: "credentials and transport need review"},
					DimPrivacy:      {Rating: RatingPoor, Reason: "data leaves this machine unless the API is local"},
					DimCacheability: {Rating: RatingGood, Reason: "responses cache well when deterministic"},
					DimDependencies: {Rating: RatingGood, Reason: "no local runtime required"},
					DimEffort:       {Rating: RatingFair, Reason: "needs a generated HTTP adapter (Part 2)"},
					DimPortability:  {Rating: RatingExcellent, Reason: "protocol-level contract travels anywhere"},
				}, Dimensions)
			},
		},
		{
			id:      StrategyMCPBridge,
			support: SupportPlanned,
			feasible: func(ctx compareContext) (bool, Rating, string, []toolchain.ToolID, map[toolchain.ToolID]toolchain.ToolID) {
				if !ctx.has(capability.KindMCP) {
					return false, RatingNA, "no MCP server configuration was detected", nil, nil
				}
				return true, RatingGood,
					"an MCP server is declared; Part 1 detects it but does not yet connect it",
					nil, nil
			},
			assess: func(ctx compareContext) []Assessment {
				return assess(map[Dimension]Assessment{
					DimFootprint:    {Rating: RatingExcellent, Reason: "GrokBot holds a tool contract"},
					DimCoverage:     {Rating: RatingGood, Reason: "whatever the server exposes"},
					DimLocal:        {Rating: RatingGood, Reason: "MCP servers commonly run locally"},
					DimExternalCost: {Rating: RatingGood, Reason: "usually no provider cost"},
					DimLatency:      {Rating: RatingGood, Reason: "one MCP round trip"},
					DimMaintenance:  {Rating: RatingGood, Reason: "protocol is standardized"},
					DimSecurity:     {Rating: RatingFair, Reason: "server permissions still apply"},
					DimPrivacy:      {Rating: RatingGood, Reason: "can stay local"},
					DimCacheability: {Rating: RatingFair, Reason: "tool results vary by call"},
					DimDependencies: {Rating: RatingFair, Reason: "requires an MCP-capable host"},
					DimEffort:       {Rating: RatingGood, Reason: "configuration is often the only work"},
					DimPortability:  {Rating: RatingGood, Reason: "standard protocol"},
				}, Dimensions)
			},
		},
		{
			id:      StrategyDockerBridge,
			support: SupportPlanned,
			feasible: func(ctx compareContext) (bool, Rating, string, []toolchain.ToolID, map[toolchain.ToolID]toolchain.ToolID) {
				if !ctx.has(capability.KindDocker) {
					return false, RatingNA, "no Dockerfile or compose file was detected", nil, nil
				}
				requires := []toolchain.ToolID{toolchain.ToolDocker}
				if !hasTool(ctx.profile, toolchain.ToolDocker) {
					return true, RatingPoor, "a container is defined but Docker is not installed on this machine", requires, nil
				}
				return true, RatingGood, "a container image is defined and Docker is available", requires, nil
			},
			assess: func(ctx compareContext) []Assessment {
				local := RatingFair
				if hasTool(ctx.profile, toolchain.ToolDocker) {
					local = RatingGood
				}
				return assess(map[Dimension]Assessment{
					DimFootprint:    {Rating: RatingGood, Reason: "GrokBot holds an image reference and arguments"},
					DimCoverage:     {Rating: RatingExcellent, Reason: "the whole application surface is available"},
					DimLocal:        {Rating: local, Reason: "executes in a container on this machine"},
					DimExternalCost: {Rating: RatingGood, Reason: "no model calls"},
					DimLatency:      {Rating: RatingPoor, Reason: "container startup dominates each call unless kept warm"},
					DimMaintenance:  {Rating: RatingFair, Reason: "image and base image both need updates"},
					DimSecurity:     {Rating: RatingFair, Reason: "container isolation helps; image contents still need review"},
					DimPrivacy:      {Rating: RatingExcellent, Reason: "stays on this machine"},
					DimCacheability: {Rating: RatingGood, Reason: "warm containers make repeated calls cheap"},
					DimDependencies: {Rating: RatingPoor, Reason: "pulls a base image and a runtime"},
					DimEffort:       {Rating: RatingFair, Reason: "compose wiring plus verification"},
					DimPortability:  {Rating: RatingGood, Reason: "images run across machines"},
				}, Dimensions)
			},
		},
		{
			id:      StrategyLocalService,
			support: SupportPlanned,
			feasible: func(ctx compareContext) (bool, Rating, string, []toolchain.ToolID, map[toolchain.ToolID]toolchain.ToolID) {
				if !ctx.has(capability.KindLocalService) {
					return false, RatingNA, "no local service was detected", nil, nil
				}
				return true, RatingFair, "a long-running local process was detected; Part 1 does not manage its lifecycle", nil, nil
			},
			assess: func(ctx compareContext) []Assessment {
				return assess(map[Dimension]Assessment{
					DimFootprint:    {Rating: RatingGood, Reason: "GrokBot holds a local endpoint contract"},
					DimCoverage:     {Rating: RatingGood, Reason: "full service surface"},
					DimLocal:        {Rating: RatingExcellent, Reason: "runs entirely on this machine"},
					DimExternalCost: {Rating: RatingExcellent, Reason: "no external calls"},
					DimLatency:      {Rating: RatingGood, Reason: "low latency while the service stays warm"},
					DimMaintenance:  {Rating: RatingFair, Reason: "process lifecycle and port management"},
					DimSecurity:     {Rating: RatingFair, Reason: "network listener needs review"},
					DimPrivacy:      {Rating: RatingExcellent, Reason: "no data leaves the machine"},
					DimCacheability: {Rating: RatingGood, Reason: "repeat queries are cheap"},
					DimDependencies: {Rating: RatingFair, Reason: "needs the runtime and a stable port"},
					DimEffort:       {Rating: RatingFair, Reason: "start/stop plus contract generation"},
					DimPortability:  {Rating: RatingPoor, Reason: "tied to a local port and host"},
				}, Dimensions)
			},
		},
		{
			id:      StrategyKnowledgeImport,
			support: SupportFull,
			feasible: func(ctx compareContext) (bool, Rating, string, []toolchain.ToolID, map[toolchain.ToolID]toolchain.ToolID) {
				if !ctx.has(capability.KindKnowledge) {
					return false, RatingNA, "no documentation was detected", nil, nil
				}
				return true, RatingGood, "documentation is present and can be searched instead of installed", nil, nil
			},
			assess: func(ctx compareContext) []Assessment {
				return assess(map[Dimension]Assessment{
					DimFootprint:    {Rating: RatingExcellent, Reason: "GrokBot receives small excerpts, never the whole documentation set"},
					DimCoverage:     {Rating: RatingFair, Reason: "only documented knowledge is covered"},
					DimLocal:        {Rating: RatingExcellent, Reason: "indexing and search run locally"},
					DimExternalCost: {Rating: RatingGood, Reason: "no provider calls for lookup"},
					DimLatency:      {Rating: RatingGood, Reason: "local index search is quick"},
					DimMaintenance:  {Rating: RatingFair, Reason: "the index must be refreshed when docs change"},
					DimSecurity:     {Rating: RatingGood, Reason: "reads text only, executes nothing"},
					DimPrivacy:      {Rating: RatingExcellent, Reason: "documents stay local"},
					DimCacheability: {Rating: RatingExcellent, Reason: "a docs index caches naturally"},
					DimDependencies: {Rating: RatingExcellent, Reason: "no new runtime required"},
					DimEffort:       {Rating: RatingGood, Reason: "index build plus a small search capability"},
					DimPortability:  {Rating: RatingExcellent, Reason: "plain text"},
				}, Dimensions)
			},
		},
		{
			id:      StrategyMicroPromptPack,
			support: SupportFull,
			feasible: func(ctx compareContext) (bool, Rating, string, []toolchain.ToolID, map[toolchain.ToolID]toolchain.ToolID) {
				return true, RatingFair, "a short instruction pack can always be produced; it trades coverage for a near-zero footprint", nil, nil
			},
			assess: func(ctx compareContext) []Assessment {
				return assess(map[Dimension]Assessment{
					DimFootprint:    {Rating: RatingExcellent, Reason: "the smallest possible GrokBot contract"},
					DimCoverage:     {Rating: RatingBlocked, Reason: "instructions cannot execute anything"},
					DimLocal:        {Rating: RatingGood, Reason: "no external execution at all"},
					DimExternalCost: {Rating: RatingFair, Reason: "the instructions live in the model context when used"},
					DimLatency:      {Rating: RatingGood, Reason: "no process or network in the path"},
					DimMaintenance:  {Rating: RatingPoor, Reason: "instructions drift silently as the source changes"},
					DimSecurity:     {Rating: RatingExcellent, Reason: "inert text, nothing to execute"},
					DimPrivacy:      {Rating: RatingGood, Reason: "small text, but it enters model context when used"},
					DimCacheability: {Rating: RatingNA, Reason: "not a deterministic artifact"},
					DimDependencies: {Rating: RatingExcellent, Reason: "none"},
					DimEffort:       {Rating: RatingExcellent, Reason: "authoring only"},
					DimPortability:  {Rating: RatingExcellent, Reason: "text travels anywhere"},
				}, Dimensions)
			},
		},
		{
			id:      StrategyGeneratedAdapter,
			support: SupportPlanned,
			feasible: func(ctx compareContext) (bool, Rating, string, []toolchain.ToolID, map[toolchain.ToolID]toolchain.ToolID) {
				needsWork := ctx.has(capability.KindLocalService) || (ctx.has(capability.KindCLI) && ctx.goalWantsTransformation())
				if !needsWork {
					return false, RatingNA, "no surface requires generated code for this goal", nil, nil
				}
				requires := []toolchain.ToolID{toolchain.ToolOpenCode}
				if _, ok := ctx.profile.AnyCodingTool(); !ok {
					return true, RatingPoor, "generated code needs a coding worker, and neither OpenCode nor Cursor was found", requires, nil
				}
				return true, RatingFair, "a coding worker can generate the adapter, or a deterministic template can", requires, nil
			},
			assess: func(ctx compareContext) []Assessment {
				return assess(map[Dimension]Assessment{
					DimFootprint:    {Rating: RatingGood, Reason: "GrokBot still receives one small contract"},
					DimCoverage:     {Rating: RatingExcellent, Reason: "the adapter can expose exactly the needed operation"},
					DimLocal:        {Rating: RatingGood, Reason: "generated adapter runs locally"},
					DimExternalCost: {Rating: RatingFair, Reason: "a coding worker may be used once to generate it"},
					DimLatency:      {Rating: RatingGood, Reason: "thin adapter overhead"},
					DimMaintenance:  {Rating: RatingFair, Reason: "generated code must track upstream changes"},
					DimSecurity:     {Rating: RatingFair, Reason: "generated code must be reviewed like any other code"},
					DimPrivacy:      {Rating: RatingGood, Reason: "generation can use a Context Pack instead of the repository"},
					DimCacheability: {Rating: RatingGood, Reason: "adapter output caches like any capability"},
					DimDependencies: {Rating: RatingFair, Reason: "adds a build step"},
					DimEffort:       {Rating: RatingPoor, Reason: "highest implementation effort of the strategies"},
					DimPortability:  {Rating: RatingGood, Reason: "the generated contract is portable"},
				}, Dimensions)
			},
		},
		{
			id:      StrategyExternalExecution,
			support: SupportFull,
			feasible: func(ctx compareContext) (bool, Rating, string, []toolchain.ToolID, map[toolchain.ToolID]toolchain.ToolID) {
				if !ctx.has(capability.KindCLI) {
					return false, RatingNA, "nothing on this machine can execute the source directly", nil, nil
				}
				return true, RatingGood, "the source already provides a command that can be invoked as-is", nil, nil
			},
			assess: func(ctx compareContext) []Assessment {
				return assess(map[Dimension]Assessment{
					DimFootprint:    {Rating: RatingGood, Reason: "no wrapper logic beyond a manifest"},
					DimCoverage:     {Rating: RatingExcellent, Reason: "the full command surface stays reachable"},
					DimLocal:        {Rating: RatingExcellent, Reason: "runs the source's own process"},
					DimExternalCost: {Rating: RatingExcellent, Reason: "no provider calls"},
					DimLatency:      {Rating: RatingGood, Reason: "process spawn per call"},
					DimMaintenance:  {Rating: RatingExcellent, Reason: "no code to maintain"},
					DimSecurity:     {Rating: RatingFair, Reason: "the source command runs with local permissions"},
					DimPrivacy:      {Rating: RatingExcellent, Reason: "stays on this machine"},
					DimCacheability: {Rating: RatingGood, Reason: "repeat invocations can be cached"},
					DimDependencies: {Rating: RatingGood, Reason: "only the source's own runtime"},
					DimEffort:       {Rating: RatingExcellent, Reason: "a manifest entry"},
					DimPortability:  {Rating: RatingFair, Reason: "requires the source to be installed already"},
				}, Dimensions)
			},
		},
		{
			id:      StrategyNoInstall,
			support: SupportFull,
			feasible: func(ctx compareContext) (bool, Rating, string, []toolchain.ToolID, map[toolchain.ToolID]toolchain.ToolID) {
				return true, RatingGood, "not installing is always a valid outcome when the goal needs less than the source provides", nil, nil
			},
			assess: func(ctx compareContext) []Assessment {
				return assess(map[Dimension]Assessment{
					DimFootprint:    {Rating: RatingExcellent, Reason: "GrokBot footprint is limited to the capability definitions that are actually needed"},
					DimCoverage:     {Rating: RatingFair, Reason: "covers only the specific need, not the whole source"},
					DimLocal:        {Rating: RatingNA, Reason: "no execution is installed"},
					DimExternalCost: {Rating: RatingExcellent, Reason: "no new cost is introduced"},
					DimLatency:      {Rating: RatingNA, Reason: "nothing to run"},
					DimMaintenance:  {Rating: RatingExcellent, Reason: "nothing to maintain"},
					DimSecurity:     {Rating: RatingExcellent, Reason: "no code is introduced"},
					DimPrivacy:      {Rating: RatingExcellent, Reason: "no data movement"},
					DimCacheability: {Rating: RatingNA, Reason: "no artifact"},
					DimDependencies: {Rating: RatingExcellent, Reason: "no dependencies added"},
					DimEffort:       {Rating: RatingExcellent, Reason: "decide and document the need"},
					DimPortability:  {Rating: RatingExcellent, Reason: "capability definitions are portable"},
				}, Dimensions)
			},
		},
	}
}

func (ctx compareContext) goalWantsTransformation() bool {
	for _, kw := range []string{"convert", "transform", "combine", "normalize", "wrap", "reshape"} {
		if strings.Contains(ctx.goal, kw) {
			return true
		}
	}
	return false
}

func recommend(ctx compareContext) (ID, string, []string, evidence.Confidence) {
	// Order encodes the product rule: install the smallest thing that satisfies
	// the goal, and prefer not installing at all when the goal needs less than
	// the source provides.
	if ctx.has(capability.KindCLI) {
		ev := ctx.evidenceFor("cli_entrypoint")
		return StrategyCLIBridge,
			"A declared command exists, so GrokBot can call a subprocess capability instead of loading the project into context.",
			ev, evidence.ConfidenceHigh
	}
	if ctx.has(capability.KindMCP) {
		return StrategyMCPBridge,
			"An MCP server is declared; a standard protocol bridge keeps the contract small.",
			ctx.evidenceFor("mcp_server_config"), evidence.ConfidenceMedium
	}
	if ctx.has(capability.KindAPI) {
		return StrategyAPIBridge,
			"An HTTP surface is documented; an API bridge exposes it without importing the project.",
			ctx.evidenceFor("openapi_spec"), evidence.ConfidenceMedium
	}
	if ctx.has(capability.KindDocker) {
		if hasTool(ctx.profile, toolchain.ToolDocker) {
			return StrategyDockerBridge,
				"A container is defined and Docker is available, so the application can run unchanged behind a small contract.",
				ctx.evidenceFor("docker_image"), evidence.ConfidenceMedium
		}
		return StrategyNoInstall,
			"A container is defined but Docker is missing; installing a container runtime is not justified for a planning step.",
			ctx.evidenceFor("docker_image"), evidence.ConfidenceLow
	}
	if ctx.has(capability.KindLocalService) {
		return StrategyGeneratedAdapter,
			"A local service exists with no declared command, so a small adapter is the smallest sufficient bridge.",
			ctx.evidenceFor("local_service_hint"), evidence.ConfidenceMedium
	}
	if ctx.has(capability.KindKnowledge) && wantsKnowledge(ctx.goal) {
		return StrategyKnowledgeImport,
			"Only documentation is present and the goal is about knowledge, so importing and searching the docs beats installing anything.",
			ctx.evidenceFor("documentation"), evidence.ConfidenceHigh
	}
	if ctx.has(capability.KindKnowledge) {
		return StrategyNoInstall,
			"Documentation alone does not justify installation unless the goal is specifically about its content.",
			ctx.evidenceFor("documentation"), evidence.ConfidenceMedium
	}
	return StrategyNoInstall,
		"No executable capability was detected, so the correct answer is not to install the source.",
		nil, evidence.ConfidenceMedium
}

func wantsKnowledge(goal string) bool {
	for _, kw := range []string{"documentation", "docs", "search", "knowledge", "guide", "reference", "answer", "explain"} {
		if strings.Contains(goal, kw) {
			return true
		}
	}
	return false
}

// Intent is a coarse, deterministic reading of the user's goal.
type Intent string

// Intents.
const (
	IntentKnowledge Intent = "knowledge"
	IntentExecution Intent = "execution"
	IntentAnalysis  Intent = "analysis"
	IntentGeneral   Intent = "general"
)

// GoalReading is the deterministic interpretation of a goal string.
type GoalReading struct {
	Intent    Intent   `json:"intent"`
	Keywords  []string `json:"keywords"`
	Questions []string `json:"unresolved_questions"`
}

// InterpretGoal reads a goal deterministically. Part 1 never calls a model to
// do this, and it never invents detail the user did not provide.
func InterpretGoal(goal string, capsList []capability.Capability) GoalReading {
	lower := strings.ToLower(goal)
	out := GoalReading{Intent: IntentGeneral}

	switch {
	case containsAny(lower, "search", "documentation", "docs", "knowledge", "guide", "reference", "answer", "explain"):
		out.Intent = IntentKnowledge
	case containsAny(lower, "run", "execute", "invoke", "call", "install", "operate", "use this"):
		out.Intent = IntentExecution
	case containsAny(lower, "review", "audit", "diagnose", "analyze", "security", "check"):
		out.Intent = IntentAnalysis
	}

	for _, kw := range goalKeywords {
		if strings.Contains(lower, kw) {
			out.Keywords = append(out.Keywords, kw)
		}
	}
	sort.Strings(out.Keywords)

	if goal == "" {
		out.Questions = append(out.Questions, "What exactly should GrokBot be able to do with this source?")
	}
	if len(capsList) == 0 {
		out.Questions = append(out.Questions, "No capability was detected; confirm whether this source exposes anything callable.")
	}
	return out
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

var goalKeywords = []string{
	"analyze", "api", "build", "call", "check", "convert", "diagnose", "docs", "docker",
	"explain", "export", "generate", "index", "inspect", "knowledge", "mcp", "migrate",
	"monitor", "query", "read", "report", "review", "run", "search", "security", "test",
	"transform",
}
