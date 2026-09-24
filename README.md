# GrokInstall

**Give GrokInstall a source. Tell it what GrokBot should do with it.**
GrokInstall figures out the smallest, safest way to make it work.

**Install the capability, not the complexity.**

GrokInstall is a universal integration compiler and advisor for GrokBot. It is
not primarily a package installer: it takes a `SOURCE` plus a user goal, and
produces understanding, capabilities, architecture alternatives, a recommended
toolchain, an installation plan, and a tiny GrokBot capability.

GrokBot stays a thin orchestrator. Whole repositories and large documentation
sets never enter its context.

## Part 1 status

Part 1 establishes the foundation and stops at planning. It implements:

| Command | What it does |
| --- | --- |
| `grokinstall inspect SOURCE` | Deterministic, evidence-backed inspection of a local directory or public GitHub repository |
| `grokinstall plan SOURCE --goal "..."` | The full pipeline: normalize, inspect, evidence, capabilities, goal, strategies, compare, toolchain, plan |
| `grokinstall compare SOURCE --goal "..."` | Compares all ten strategies across twelve dimensions |
| `grokinstall doctor` | Environment and state checks with actionable fixes |
| `grokinstall usage` | Measured usage: operations, cache hits/misses, context pack bytes |

All commands support `--json`.

**Nothing is installed or executed in Part 1.** Plans state exactly which
strategies are implemented, planned, or unsupported.

## Architecture

```
SOURCE
  ↓
NORMALIZE          source
  ↓
INSPECT            inspect          never executes project code
  ↓
EVIDENCE           evidence         every conclusion is backed
  ↓
CAPABILITIES       capability       only what evidence supports
  ↓
GOAL               strategy         deterministic interpretation
  ↓
STRATEGIES         strategy         ten candidates, always compared
  ↓
COMPARE            strategy         twelve qualitative dimensions
  ↓
TOOLCHAIN          toolchain        required / recommended / optional / irrelevant / missing / substitute
  ↓
PLAN               plan             draft manifest + bounded Context Pack
```

Supporting packages: `contextpack` (what a worker is allowed to see),
`worker` (the external worker protocol), `manifest` (the capability contract),
`registry` and `cache` (atomic JSON state), `diagnostics` (doctor), `usage`
(JSONL observability), `cli` (commands).

## Sources

Part 1 supports:

- local repositories
- public GitHub repositories (cloned into controlled temporary storage via
  `git`; the commit SHA becomes the cache identity)

GitLab URLs and other remote hosts are recognized and reported as unsupported
rather than guessed. Website, API, MCP, Docker, package and plain-English idea
sources are planned for later parts.

## Detection

Inspection detects CLI entrypoints, API/OpenAPI surfaces, MCP configuration,
Docker and compose definitions, documentation, local services, tests, CI
workflows, licenses, environment templates and lockfiles — without running a
single line of the source.

### Source files are untrusted data

- README and source text never become instructions to GrokInstall.
- Instruction-like text is recorded as evidence, never obeyed.
- Install scripts, `postinstall` hooks, `sudo` usage and download commands are
  surfaced for review, never executed.
- Secret-like files are reported; their contents are never imported.
- Symlinks are not followed, and inspection is bounded by file count, per-file
  size and total size limits.

## Toolchain resolution

GrokInstall detects `git`, `gh`, `go`, `node`, `pnpm`, `npm`, `python`, `uv`,
`docker`, `opencode`, `cursor`, `vercel` and `ollama`, and reports each as
required, recommended, optional, irrelevant, missing, or satisfied by a
substitute.

**It never recommends installing everything.** OpenCode, Cursor, ChatGPT,
Claude, Grok, Docker, Vercel and Ollama are optional capabilities, not
prerequisites.

## Context Packs

A `ContextPack` carries only the goal, relevant source metadata, relevant
manifest evidence, discovered capabilities, unresolved questions and
constraints. It is the only structure handed to a reasoning or coding worker,
which is what keeps whole repositories out of model context.

## Worker protocol

Any external execution — a local script, a CLI, or a future provider — uses the
same JSON request/response contract:

```json
{ "task": "...", "input": {}, "context_pack": {}, "constraints": {} }
```

```json
{ "ok": true, "result": {}, "artifacts": [], "warnings": [] }
```

The local subprocess worker is implemented in Part 1, with explicit timeouts
and bounded stdout/stderr. OpenCode, Cursor, ChatGPT, Claude, Grok and Ollama
are registered as recognized-but-not-integrated, so their absence is reported
honestly instead of silently.

## Capability manifest

Every plan includes a draft manifest using schema `grokinstall/v1`: name,
version, source, goal, capabilities, execution, inputs, outputs, grokbot, cache,
security and provenance/evidence. The manifest is what GrokBot understands, not
the implementation.

## State

```
~/.grokinstall/
├── config.json
├── registry.json
├── manifests/
├── adapters/
├── receipts/
├── cache/
├── diagnostics/
└── logs/
    └── usage.jsonl
```

State is plain JSON/JSONL written atomically. `GROKINSTALL_HOME` or
`--state-dir` overrides the location.

## Caching

Deterministic inspection is cached by stable identity: repository plus commit
SHA, or a content hash for non-git sources, combined with the inspection
version and option fingerprint. Subjective planning decisions are not cached
until their inputs are fully represented in a key.

## Build and test

```bash
go test ./...
go vet ./...
go build ./cmd/grokinstall
```

## License

MIT
