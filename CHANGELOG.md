# Changelog

All notable changes to GrokInstall are recorded here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and
this project adheres to semantic versioning once the CLI is stable.

## [0.2.0] - Part 2: installation and capability runtime

GrokInstall becomes a working capability installer and runtime. Installing a
capability is not the same as installing upstream software: GrokInstall
registers a contract, a thin adapter when one is genuinely needed, a registry
entry and a receipt.

### Added

- `grokinstall install SOURCE --goal "..."` — staged installation
  (plan, stage, verify, commit to registry). A source that needs no
  installation is a successful outcome, not a failure.
- `grokinstall run NAME` — universal capability runtime with a stable envelope:
  `{"ok":true,"result":{},"artifacts":[],"warnings":[]}`. Input via `--input`
  or stdin.
- `grokinstall test NAME` — real smoke test covering manifest validity, the
  execution target, the adapter and an actual bounded launch.
- `grokinstall list`, `grokinstall info NAME`, `grokinstall capabilities`,
  `grokinstall grokbot NAME`, `grokinstall uninstall NAME`.
- `--dry-run` on `install` and `uninstall`, reporting the strategy, the files
  that would be created, registry changes, adapter generation, external
  commands, dependencies, the verification plan and security concerns.
- `runtime` package: direct argv execution (never shell-string
  interpolation), explicit cwd and environment, hard timeouts, bounded stdout
  and stderr, and structured error codes.
- `knowledge` package: a bounded, deterministic stdlib text index and search
  over a source's documentation. No embeddings, no vector database.
- `receipt` package: a real record of what each install changed, including
  file ownership, executed commands, introduced dependencies and verification
  results.
- `registry`: register, list, lookup, update and remove, with duplicate
  rejection, corruption detection and stale-manifest reporting.
- Inspection now records where a declared CLI entrypoint actually lives
  (`entrypoint_paths`), so an installer can invoke it without guessing.
- Strategy recommendations are goal-aware: an explicit documentation-retrieval
  goal prefers knowledge import over an available CLI.

### Changed

- The manifest is now an installed-capability contract carrying execution
  type, command, argv, cwd, environment, timeout and bounds, plus a required
  support level. A manifest may never claim `execution.supported` it cannot
  deliver.
- Registry entries point at manifests instead of duplicating them.
- Usage telemetry now records installs, runs, tests, uninstalls, capability
  input and output bytes, and GrokBot contract bytes. Byte counts are never
  presented as token counts.

### Safety

- A supported strategy that cannot resolve an executable fails installation
  rather than silently degrading to a plan.
- Failed verification discards the staged files and registers nothing.
- Uninstall removes only paths inside GrokInstall's own state directory, and
  preserves upstream sources, receipts, logs and unrelated files.
- Self-installation is refused with guidance instead of duplicating itself.

## [0.1.0] - Part 1: planning foundation

The architecture was rebased on a universal integration compiler. Part 1 stops
at planning and implements `inspect`, `plan`, `compare`, `doctor` and `usage`
over local directories and public GitHub repositories, with evidence-backed
findings, bounded Context Packs, ten compared strategies across twelve
dimensions, and a toolchain resolver that never recommends installing
everything.
