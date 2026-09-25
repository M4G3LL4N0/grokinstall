# Concepts

## Source

A normalized reference: a local directory or a public GitHub repository. A
source has a **canonical identity** used for caching, distinct from its contents.

## Identity

The stable key for a source.

- git repository: commit SHA
- non-git directory: a content hash of the inspected tree

Identity plus the inspection version and option fingerprint forms the cache
key. A changed commit is a different source as far as the cache is concerned.

## Evidence

Every conclusion GrokInstall reaches is backed by evidence: a finding, a
confidence level, and the observations that produced it.

```json
{
  "finding": "cli_entrypoint",
  "confidence": "high",
  "evidence": ["package.json bin field declares command \"widget\" at \"bin/widget.sh\""]
}
```

Evidence is the currency of the product. A conclusion without evidence does not
survive into a plan.

## Capability

Something GrokBot can call. A capability exists only when an inspected artifact
backs it. A repository with a `package.json` `bin` field has a CLI capability;
one with only prose does not.

## Strategy

A way to integrate a capability. Ten are always compared:

```text
cli_bridge  api_bridge  mcp_bridge  docker_bridge  local_service
knowledge_import  micro_prompt_pack  generated_adapter
external_execution  no_install
```

`no_install` is a first-class recommendation. Installing less is frequently the
right answer.

## Support level

What GrokInstall can actually do with a strategy today.

| Level | Meaning |
| --- | --- |
| `supported` | installed, verified, runnable |
| `experimental` | runs, with a narrower or less-tested path |
| `plan_only` | detected and compared; persisted as a plan, never installed |
| `unsupported` | GrokInstall will not attempt it |
| `none` | a deliberate no-install decision |

A plan is not an installed capability. `grokinstall capabilities` shows runnable
capabilities by default so GrokBot is never handed something it cannot call.

## Manifest

The capability contract. It is what GrokBot understands, not the
implementation. It carries the name, source, goal, strategy, support level,
execution details, input and output schemas, the GrokBot handoff, cache policy,
security notes and provenance.

A manifest may never claim `execution.supported` unless it passed verification.

## Runtime

The universal execution protocol.

```bash
grokinstall run NAME --input '{...}'
```

```json
{ "ok": true, "result": {}, "artifacts": [], "warnings": [] }
```

The runtime owns safety: direct argv, explicit cwd and environment, a hard
timeout, and bounded output.

## Provisioning

Obtaining a missing executable, under an explicit policy. See
[provisioning](provisioning.md).

## Runtime (owned)

A GrokInstall-owned directory containing a provisioned executable and its
provenance:

```text
~/.grokinstall/runtimes/<capability>/
├── bin/<tool>
└── metadata.json
```

"Runtime" means two different things in GrokInstall: the execution protocol, and
a provisioned executable's home. Both are owned by GrokInstall; neither touches
your `PATH`.

## Receipt

The record of what an install changed: install id, source identity and commit,
goal, strategy, files created with ownership, commands executed, dependencies
introduced, provisioning provenance, verification results and the outcome.

Receipts make uninstall, audit and repair possible, and they are preserved
after uninstall as the audit trail.

## Context Pack

The bounded payload an optional reasoning worker may see: goal, relevant source
metadata, relevant evidence, capabilities, unresolved questions and constraints.
Secret material is redacted. Whole repositories are never handed to a model.
