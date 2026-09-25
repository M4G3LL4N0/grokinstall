# Roadmap

GrokInstall's promise is stable: **install the capability, not the complexity.**
The roadmap adds capability without weakening that.

No dates are committed. Work lands when it is verified.

## v0.1 — current

The verified foundation.

- local and public GitHub inspection
- CLI bridge, knowledge import, external execution, no-install
- safe provisioning: existing executable, GitHub release, Go source build
- capability registry and universal runtime
- GrokBot contracts and Context Packs
- staged installation, receipts, rollback and `dirty` state
- `diagnose`, `audit`, `doctor`, `usage`
- safe uninstall
- secret redaction and an adversarial suite

## v0.2 — runtime bridges and repair

- **OpenAPI / API bridge**: execute a documented HTTP surface through a
  generated contract, with narrow, audited request shaping.
- **MCP bridge**: connect a detected MCP server through the standard protocol.
- **Docker bridge**: run a detected container behind a capability contract, with
  explicit image and permission review.
- **Safe package-manager executors**: npm, Python and Cargo routes that are
  honest about lifecycle scripts and build backends, and never run them without
  the matching authorization.
- **Better argv schemas**: per-field argument mapping so capabilities can expose
  real flags instead of a single JSON blob.
- **Repair and update**: diagnose findings become actions; update a capability
  from a new upstream version and re-verify.

## v0.3 — builders and optional reasoning

- **OpenCode builder integration**: generate an adapter with a coding agent
  operating directly on a local checkout, receiving a Context Pack rather than a
  serialized repository.
- **Cursor builder integration**, where appropriate.
- **Optional reasoning workers**: ChatGPT, Claude and Grok, strictly opt-in and
  never required for a deterministic path.
- **Idea to capability**: turn a plain-English goal into a plan and, where
  warranted, a capability.
- **Recipe format**: a portable, reviewable description of a capability
  integration.

## Later

- a public recipe and catalog ecosystem
- a GUI
- cross-machine capability portability

## Explicit non-goals

Some things GrokInstall will not do, because they would contradict its purpose:

- load whole repositories into model context
- run a project's install scripts because a README says to
- make GrokInstall a general-purpose package manager
- present a detected-but-unimplemented integration as working
- offer a generic "yes, trust everything" flag
