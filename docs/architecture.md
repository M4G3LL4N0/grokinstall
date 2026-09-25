# Architecture

GrokInstall keeps three concerns apart: **understanding a source**, **deciding
what to do**, and **executing the result**. Collapsing them is how integration
tools end up running arbitrary project code.

```text
                    ┌───────────────┐
  SOURCE + GOAL ───▶│    source     │ normalize to a canonical identity
                    └───────┬───────┘
                            ▼
                    ┌───────────────┐
                    │   inspect     │ bounded read; never executes source
                    └───────┬───────┘
                            ▼
                    ┌───────────────┐
                    │   evidence    │ findings with confidence + backing
                    └───────┬───────┘
                            ▼
                    ┌───────────────┐
                    │  capability   │ only what evidence supports
                    └───────┬───────┘
                            ▼
                    ┌───────────────┐
                    │   strategy    │ ten candidates, twelve dimensions
                    └───────┬───────┘
                            ▼
                    ┌───────────────┐
                    │  provision    │ smallest safe route to an executable
                    └───────┬───────┘
                            ▼
                    ┌───────────────┐
                    │  installer    │ stage → verify → commit → register
                    └───────┬───────┘
                            ▼
                    ┌───────────────┐
                    │   runtime     │ direct argv, bounded, timed out
                    └───────────────┘
```

## Packages

| Package | Responsibility | Depends on |
| --- | --- | --- |
| `source` | normalize a SOURCE reference | — |
| `evidence` | findings with confidence and backing | — |
| `inspect` | bounded, deterministic source reading | source, evidence |
| `capability` | derive capabilities from evidence | evidence |
| `strategy` | compare candidate strategies | capability, evidence |
| `toolchain` | detect tools; resolve substitutions | — |
| `plan` | assemble the planning pipeline | most of the above |
| `provision` | policy, candidates, owned runtimes | provision/github, provision/gobuild |
| `runtime` | universal capability execution | — |
| `knowledge` | bounded index and deterministic search | — |
| `manifest` | the capability contract | evidence, redact |
| `registry` | installed capabilities, plans, states | cache |
| `cache` | content-addressed cache, atomic state | — |
| `receipt` | the record of an install | — |
| `diagnose` | why is this not working | registry, manifest, provision |
| `audit` | deterministic integration review | registry, manifest, receipt |
| `worker` | external worker protocol | contextpack |
| `contextpack` | the bounded worker payload | evidence, capability |
| `usage` | measured observability | — |
| `cli` | commands | most of the above |

The dependency graph is acyclic. `inspect` never imports `installer`;
`strategy` never imports `runtime`. This is what keeps a change in one area from
quietly weakening another.

## The install transaction

```text
plan
  → provision stage     obtain an executable, if one is needed
  → adapter stage       write generated data, if a strategy needs it
  → manifest stage      write the contract
  → verify              actually run it, under bounds
  → commit files        move into final state
  → register            add the registry entry
  → write receipt       record what happened
```

Provisioning happens **before** the manifest is built, so a manifest can never
point at a staging path that will be deleted.

Failure handling is explicit:

| Failure | Outcome |
| --- | --- |
| before verify | stage discarded, nothing registered, failed receipt kept |
| verify | stage discarded, nothing registered |
| commit, rollback complete | nothing registered, failed receipt |
| commit, rollback incomplete | capability marked `dirty`, never reported as success |
| policy refusal | nothing registered, plan persisted outside the registry |

## State

```text
~/.grokinstall/
├── config.json      user configuration
├── registry.json    installed capabilities (points at manifests)
├── manifests/       one contract per capability
├── adapters/        generated data
├── receipts/        install records, preserved after uninstall
├── plans/           plans — never capabilities
├── runtimes/        GrokInstall-owned provisioned executables
├── staging/         in-progress work
├── cache/           deterministic inspection cache
├── diagnostics/
└── logs/usage.jsonl measured counters
```

Every file is written atomically: a temporary file, then a rename. A reader
never observes a partial file.
