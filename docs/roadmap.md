# Roadmap

The canonical roadmap is [ROADMAP.md](../ROADMAP.md) at the repository root.

## v0.1 — current

Inspection (local and public GitHub), CLI/knowledge/external/no-install
strategies, safe provisioning (existing executable, GitHub release, Go source
build), registry and runtime, GrokBot contracts, Context Packs, staged
installation with receipts and rollback, diagnose, audit, doctor, usage, safe
uninstall, secret redaction, adversarial suite.

## v0.2

- OpenAPI / API runtime bridge
- MCP runtime bridge
- Docker runtime bridge
- safe package-manager executors (npm, Python, Cargo)
- better argv schemas, so capabilities expose real flags
- repair and update

## v0.3

- OpenCode builder integration
- Cursor builder integration
- optional ChatGPT, Claude and Grok reasoning workers
- idea to capability planning
- a portable recipe format

## Later

- public recipe and catalog ecosystem
- GUI
- cross-machine capability portability

## Non-goals

GrokInstall will not load whole repositories into model context, run install
scripts because a README says to, become a general package manager, present a
detected-but-unimplemented integration as working, or offer a generic "trust
everything" flag.
