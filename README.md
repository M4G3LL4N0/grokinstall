# GrokInstall

**Install the capability, not the complexity.**

Give GrokInstall a project and tell it what you want GrokBot to do.

GrokInstall inspects the source, discovers the useful capability, compares
integration strategies, resolves a safe toolchain, installs and verifies the
smallest useful integration, and generates a compact contract GrokBot can invoke
without loading the entire project into context.

```text
GitHub repo
     ↓
GrokInstall
     ↓
inspect → compare → provision → verify
     ↓
tiny capability contract
     ↓
GrokBot
```

GrokInstall is **the integration guru for GrokBot**. It is not a package
installer. It decides what should *not* be installed just as carefully as what
should be, and "do not install" is a first-class answer.

---

## Why GrokInstall

Pointing a model at a repository is expensive, fragile and unsafe. The useful
thing about a project is usually a handful of operations, not its source.

| Without GrokInstall | With GrokInstall |
| --- | --- |
| The whole repository enters model context | GrokBot receives a small contract |
| Every integration is a bespoke prompt | Integrations are compared and chosen |
| Missing tools are improvised | The toolchain is resolved or explicitly unresolved |
| Install scripts run because a README says so | Scripts are recorded as evidence, never executed blindly |
| A failed install leaves a half-installed state | Nothing is registered until verification passes |
| "Why is this broken?" is guesswork | `diagnose` answers with evidence and a fix |

## Quick Start

Requires Go 1.24 or later to build from source. A prebuilt binary is available
from [GitHub releases](https://github.com/M4G3LL4N0/grokinstall/releases).

```bash
# Look at a project before installing anything
grokinstall inspect https://github.com/sharkdp/bat

# See how it would integrate, and why
grokinstall compare https://github.com/sharkdp/bat \
  --goal "Let GrokBot use bat to inspect text files"

# Install the capability
grokinstall install https://github.com/sharkdp/bat \
  --goal "Let GrokBot use bat to inspect text files"

# What can GrokBot call?
grokinstall capabilities

# The contract for one capability
grokinstall grokbot bat.search

# Call it
grokinstall run bat.search --input '{}'
```

## How It Works

```text
SOURCE + GOAL
  ↓
INSPECT         bounded, deterministic, never executes source code
  ↓
EVIDENCE        every conclusion backed by what was actually observed
  ↓
CAPABILITIES    only what evidence supports
  ↓
STRATEGIES      ten candidates, always compared
  ↓
PROVISION       the smallest safe route to a working executable
  ↓
VERIFY          the capability actually runs
  ↓
CONTRACT        a small manifest GrokBot can invoke
```

## Supported Today

Verified against the Part 3 release and the `sharkdp/bat` flagship run.

**Sources**
- local repositories
- public GitHub repositories

**Integration strategies**
- `cli_bridge` — wrap an existing command behind a JSON contract
- `knowledge_import` — a bounded local index with deterministic search
- `external_execution` — register an externally managed command
- `no_install` — a first-class successful recommendation

**Provisioning**
- an already-installed compatible executable
- a GitHub release artifact (checksum verified when upstream publishes one)
- a Go source build into a GrokInstall-owned runtime

**Operations**
- capability registry with explicit lifecycle states
- universal capability runtime
- GrokBot contracts, measured and bounded
- Context Packs that keep whole repositories out of model context
- staged installation with rollback and a `dirty` state
- receipts recording real changes and ownership
- `diagnose`, `audit`, `doctor`, `usage`
- safe uninstall
- inspection caching keyed by commit or content hash
- secret redaction across context packs, manifests and contracts
- adversarial source handling

## Detected / Planned

These are **detected today**. Full installation support is coming next. They are
not equivalent to the verified integrations above.

| Strategy | Status |
| --- | --- |
| `api_bridge` | detected and compared; plan-only |
| `mcp_bridge` | detected and compared; plan-only |
| `docker_bridge` | detected and compared; plan-only |
| `local_service` | detected and compared; plan-only |
| `generated_adapter` | detected and compared; plan-only |
| `micro_prompt_pack` | detected and compared; plan-only |

**Also planned**
- npm / Python / Cargo package-manager execution (discovered and classified
  correctly; no executor, because these can run arbitrary build code)
- OpenCode, Cursor, ChatGPT, Claude and Grok worker integrations
- Homebrew formula

GrokInstall does **not** claim to support every GitHub project, to install
anything with one click, or to work with every tool. It refuses when it cannot
do something safely, and says why.

## Examples

### It provisions a missing executable

```text
SOURCE
sharkdp/bat

GOAL
Expose the useful CLI capability to GrokBot.

GROKINSTALL DISCOVERED
A CLI capability, but no appropriate existing executable.

PROVISIONING
GitHub release artifact (v0.26.1, aarch64-apple-darwin).

INSTALL LOCATION
GrokInstall-owned runtime, not your PATH.

RESULT
A verified, runnable capability.

GROKBOT RECEIVES
A compact contract (measured at 443 bytes in the verified run), not the
repository.

UNINSTALL
The owned runtime is removed. No system or global package state was ever
touched.
```

This worked because `bat` publishes a signed-off release artifact for this
platform. It is not a claim that every repository can be provisioned this way.

### It says no

```text
SOURCE
pallets/click

GROKINSTALL FOUND
A Python installation route, which may execute the project's build backend.

SAFE AUTOMATIC POLICY
Blocked.

RESULT
Nothing installed. Nothing registered.

GROKINSTALL EXPLAINS
The required authorization (--allow-install-scripts) and the alternatives.
```

See [the click refusal](docs/provisioning.md#worked-example-a-successful-provision) and
[the refusal example](docs/provisioning.md#worked-example-b-a-safe-refusal) in the
provisioning docs.

## Safe Provisioning

GrokInstall can obtain a missing executable, but it is not a general package
manager. Routes are compared in safety order:

```text
1. already-installed compatible executable      changes nothing
2. verifiable upstream release artifact        owned runtime only
3. reproducible build into an owned runtime    `go build` only
4. language package manager                   needs authorization
5. system package manager                     needs authorization
6. user-provided command                      explicit
7. unresolved requirement                    reported, not guessed
```

```bash
--provision=safe      # default: only routes needing no extra authorization
--provision=never     # never provision
--provision=prompt    # behave like safe and surface decisions to the caller
```

Approvals name a specific risk class. There is deliberately no generic `--yes`:

| Flag | Unlocks |
| --- | --- |
| `--allow-install-scripts` | package lifecycle scripts and build backends |
| `--allow-source-build` | compiling untrusted source |
| `--allow-system-package-manager` | machine-wide package changes |

## GrokBot Integration

GrokBot needs three commands and never needs the repository.

```bash
# 1. What can I call?
grokinstall capabilities --json

# 2. How do I call it?
grokinstall grokbot NAME --json

# 3. Call it
grokinstall run NAME --input '{...}'
```

On failure:

```bash
grokinstall diagnose NAME --json
```

`grokinstall capabilities` shows only **runnable** capabilities by default, so
GrokBot is never handed something it cannot call. See
[docs/GROKBOT-HANDOFF.md](docs/GROKBOT-HANDOFF.md).

## Context Packs

When optional reasoning or coding assistance is needed, it receives a
`ContextPack`: goal, relevant source metadata, relevant evidence, capabilities,
unresolved questions and constraints. Secret material is redacted. Whole
repositories are never handed to a model.

## Security Model

Source repositories are treated as hostile. See [SECURITY.md](SECURITY.md) for
the full model and [docs/security.md](docs/security.md) for the mechanics.

- README and source text are **evidence, never authority**.
- Inspection is bounded and never executes project code or install scripts.
- Commands are executed with direct argv; no shell-string interpolation exists.
- Process output is bounded and every capability has a hard timeout.
- Release archives are extracted under strict rules: traversal, absolute paths,
  symlinks, hardlinks and decompression bombs are rejected.
- Provisioned runtimes record per-file hashes; `audit` and `diagnose` detect
  modification.
- Uninstall removes only what GrokInstall created.

What GrokInstall **cannot** guarantee: that a source's contents are benign, that
an upstream release is trustworthy when upstream publishes no checksum, or that a
capability you authorized is safe to run.

## CLI Reference

```text
Understand a source
  inspect SOURCE           inspect a local directory or public GitHub repository
  plan SOURCE --goal       evidence-backed integration plan, changes nothing
  compare SOURCE --goal    compare strategies across twelve dimensions

Install and use a capability
  install SOURCE --goal     install a capability, provisioning only when safe
  run NAME                  invoke a capability
  test NAME                 smoke test a capability
  list                      installed capabilities
  info NAME                 full detail for one capability
  capabilities              runnable capabilities, for GrokBot
  grokbot NAME              the contract GrokBot should follow

Operate an installation
  diagnose NAME             why is this not working, with evidence
  audit NAME                deterministic review of one integration
  doctor                    environment and state checks with fixes
  usage                     measured counters
  uninstall NAME            remove only GrokInstall-owned resources

Project
  version                   version, commit and build information
```

`--json` is available on every command. `--dry-run` is available on `install`
and `uninstall`. Exit codes: `0` success, `1` failure, `2` usage error.

## Diagnostics

```bash
grokinstall diagnose bat.search
```

```text
[CRITICAL]
SYMPTOM
  provisioned runtime was modified after installation

EVIDENCE
  runtime: ~/.grokinstall/runtimes/bat.search
  bin/bat: content changed

ROOT CAUSE
  files no longer match the hashes recorded at provisioning time

CONFIDENCE
  high

FIX
  reinstall the capability to restore a verified runtime

VERIFY
  grokinstall audit bat.search
```

```bash
grokinstall audit bat.search
```

Audits manifest validity, receipt consistency, runtime ownership and integrity,
permissions, checksum provenance, adapter integrity and the GrokBot contract.
Severity is not inflated: a clean install produces no findings above `info`.

## Architecture

```text
source → inspect → evidence → capability → strategy → toolchain → plan
                                                       ↓
                             provision → runtime → verify → registry
```

| Package | Responsibility |
| --- | --- |
| `source` | normalize a SOURCE reference into a canonical identity |
| `inspect` | bounded, deterministic reading of a source; never executes it |
| `evidence` | findings with confidence and backing evidence |
| `capability` | capabilities that evidence actually supports |
| `strategy` | ten candidate strategies across twelve dimensions |
| `toolchain` | detect tools; resolve or report a substitution |
| `provision` | policy, candidates, owned runtimes, provisioning |
| `plan` | the Part 1 planning pipeline |
| `runtime` | universal capability execution with safety bounds |
| `knowledge` | bounded local index and deterministic search |
| `manifest` | the capability contract |
| `registry` | installed capabilities, plans, lifecycle states |
| `cache` | content-addressed inspection cache and atomic state |
| `diagnose` / `audit` | evidence-backed answers about an installation |
| `worker` | external worker protocol, provider-agnostic |
| `contextpack` | the bounded payload a worker may see |
| `usage` | measured observability |

## Installation

**Release binary (recommended).** Download, verify the checksum, run:

```bash
# macOS (Apple silicon)
curl -LO https://github.com/M4G3LL4N0/grokinstall/releases/download/v0.1.0/grokinstall_v0.1.0_darwin_arm64.tar.gz
curl -LO https://github.com/M4G3LL4N0/grokinstall/releases/download/v0.1.0/SHA256SUMS
shasum -a 256 -c SHA256SUMS --ignore-missing
tar xzf grokinstall_v0.1.0_darwin_arm64.tar.gz
sudo mv grokinstall /usr/local/bin/
```

**Go install**

```bash
go install github.com/M4G3LL4N0/grokinstall/cmd/grokinstall@latest
```

**Build from source**

```bash
git clone https://github.com/M4G3LL4N0/grokinstall
cd grokinstall
go build -o grokinstall ./cmd/grokinstall
```

We deliberately do not lead with `curl ... | sh`. GrokInstall verifies release
artifacts; its own installer should not be less careful.

## Development

```bash
go test ./...          # unit and integration tests
go vet ./...
go test -race ./...
go build ./...

# live network tests are opt-in
GROKINSTALL_NETWORK_TESTS=1 go test ./internal/inspect/ -run PublicGitHub
```

See [CONTRIBUTING.md](CONTRIBUTING.md).

## Roadmap

See [ROADMAP.md](ROADMAP.md). Short version: v0.1 is this release, v0.2 targets
the API/MCP/Docker runtime bridges and repair, v0.3 targets builder
integrations.

## Contributing

Issues and pull requests are welcome. New provisioners and strategies must ship
with adversarial tests — see [CONTRIBUTING.md](CONTRIBUTING.md).

## License

[MIT](LICENSE)
