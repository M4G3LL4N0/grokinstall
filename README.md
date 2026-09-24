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

## What GrokInstall does

GrokInstall takes a `SOURCE` plus a user goal, and turns it into a verified,
registered GrokBot capability:

```text
SOURCE + GOAL
  ↓
INSPECT            never executes project code
  ↓
UNDERSTAND         evidence-backed findings
  ↓
COMPARE            ten strategies, twelve dimensions
  ↓
STAGE              manifest, adapter, receipt
  ↓
VERIFY             the capability actually runs
  ↓
REGISTER           a tiny GrokBot capability contract
  ↓
RUN                one command, bounded output
```

**Install the capability, not the complexity.** Installing a capability means
registering a contract, a thin adapter when one is genuinely needed, a registry
entry and a receipt. It does not mean installing an upstream software stack,
and GrokInstall never runs a project's install scripts.

## Commands

| Command | What it does |
| --- | --- |
| `grokinstall inspect SOURCE` | Deterministic, evidence-backed inspection |
| `grokinstall plan SOURCE --goal "..."` | Full planning pipeline, no changes |
| `grokinstall compare SOURCE --goal "..."` | Strategy comparison across twelve dimensions |
| `grokinstall install SOURCE --goal "..."` | Install a capability integration |
| `grokinstall run NAME` | Invoke a capability |
| `grokinstall test NAME` | Smoke test a capability |
| `grokinstall list` | Installed capabilities |
| `grokinstall info NAME` | Everything worth knowing about one capability |
| `grokinstall capabilities` | Machine-facing discovery for GrokBot |
| `grokinstall grokbot NAME` | Smallest sufficient GrokBot contract |
| `grokinstall uninstall NAME` | Remove only GrokInstall-owned resources |
| `grokinstall doctor` | Environment and state checks with fixes |
| `grokinstall usage` | Measured usage counters |

`--json` is supported everywhere. `--dry-run` is supported on `install` and
`uninstall`. Exit codes: `0` success, `1` failure, `2` usage error.

## Strategy support

| Strategy | Support |
| --- | --- |
| `cli_bridge` | **supported** — registers an existing command behind a JSON contract |
| `knowledge_import` | **supported** — bounded local index and deterministic search |
| `external_execution` | **supported** — registers an externally managed command |
| `no_install` | **supported** — a successful, first-class outcome |
| `api_bridge` | plan-only — detected and compared, not yet invoked |
| `mcp_bridge` | plan-only — detected and compared, not yet invoked |
| `docker_bridge` | plan-only — detected and compared, not yet invoked |
| `local_service` | plan-only — detected and compared, not yet invoked |
| `generated_adapter` | plan-only — needs a configured builder worker |
| `micro_prompt_pack` | plan-only — detected and compared |

A strategy that promises a runnable capability but cannot resolve an executable
**fails installation**. It never silently degrades to a plan.

## Universal capability runtime

Every capability is invoked the same way and returns the same envelope:

```bash
grokinstall run repo.search --input '{"query":"authentication"}'
```

```json
{ "ok": true, "result": {}, "artifacts": [], "warnings": [] }
```

Input may also be piped on stdin. The runtime owns safety: direct argv
execution (never shell-string interpolation), explicit working directory and
environment, hard timeouts, bounded stdout/stderr, and structured error codes
such as `timeout`, `nonzero_exit`, `output_too_large` and `malformed_output`.

## Knowledge capabilities

A documentation source becomes a bounded local index rather than a copy of the
documentation in GrokBot's context:

```bash
grokinstall install ./docs --goal "Let GrokBot retrieve relevant documentation"
grokinstall run project.docs --input '{"query":"authentication","limit":3}'
```

```json
{
  "matches": [
    { "file": "docs/auth.md", "title": "Authentication", "excerpt": "...", "score": 12.4 }
  ]
}
```

Indexing is bounded by file count, per-file size and total size. Binary content
is rejected. Excerpts are bounded. There are no embeddings and no vector
database: the ranking is a plain term-frequency score you can explain.

## Staged installation

```text
PLAN → STAGE → VERIFY → COMMIT TO REGISTRY
```

Nothing is written into final state until verification passes. A failed
verification discards the stage, registers nothing, and still leaves a receipt
so the attempt stays auditable.

## Receipts and uninstall

Every mutating install writes a receipt recording the install id, source
identity and commit, goal, strategy, files created (with ownership), commands
executed, dependencies introduced, verification results and the outcome.

Uninstall removes only what GrokInstall created — its manifest, its adapter and
its registry entry. Upstream repositories, user data, receipts, logs and
unrelated files are never touched. The receipt is preserved and marked
uninstalled.

## GrokBot discovery

GrokBot needs only three commands to operate normally:

```bash
grokinstall capabilities --json   # enumerate callable capabilities
grokinstall grokbot NAME --json  # get the contract for one
grokinstall run NAME ...          # invoke it
```

No README, repository tree, package manifests, source code or strategy
internals are required.

## Source files are untrusted data

- README and source text never become instructions to GrokInstall.
- Instruction-like text is recorded as evidence, never obeyed.
- Install scripts, `postinstall` hooks, `sudo` usage and download commands are
  surfaced for review, never executed.
- Secret-like files are reported; their contents are never imported.
- Symlinks are not followed, and inspection is bounded by file count,
  per-file size and total size.

## Toolchain resolution

GrokInstall detects `git`, `gh`, `go`, `node`, `pnpm`, `npm`, `python`, `uv`,
`docker`, `opencode`, `cursor`, `vercel` and `ollama`, and reports each as
required, recommended, optional, irrelevant, missing, or satisfied by a
substitute.

**It never recommends installing everything.** OpenCode, Cursor, ChatGPT,
Claude, Grok, Docker, Vercel and Ollama are optional capabilities, not
prerequisites. Builder preference is: no generated code if unnecessary, then a
deterministic adapter generator, then an existing local builder, then OpenCode,
then Cursor, then a configured worker, then an explicit unresolved requirement.

## Context Packs

A `ContextPack` carries only the goal, relevant source metadata, relevant
manifest evidence, discovered capabilities, unresolved questions and
constraints. It is the only structure handed to a reasoning or coding worker,
which is what keeps whole repositories out of model context.

## State

```
~/.grokinstall/
├── config.json
├── registry.json
├── manifests/
├── adapters/
├── receipts/
├── staging/
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
version and option fingerprint. Capability **execution results are never
cached** — caching is a semantic decision, not a convenience.

## Build and test

```bash
go test ./...
go vet ./...
go test -race ./...
go build ./cmd/grokinstall
```

## License

MIT

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

Supporting packages: `runtime` (universal capability execution), `knowledge`
(bounded local search), `installer` (staged install), `receipt` (audit trail),
`contextpack` (what a worker is allowed to see), `worker` (the external worker
protocol), `manifest` (the capability contract), `registry` and `cache` (atomic
JSON state), `diagnostics` (doctor), `usage` (JSONL observability), `cli`
(commands).

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
