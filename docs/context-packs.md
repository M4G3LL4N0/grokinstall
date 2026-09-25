# Context Packs

A `ContextPack` is the only structure GrokInstall hands to an external reasoning
or coding worker. It exists so a worker never receives a whole repository.

## Contents

```json
{
  "schema": "grokinstall/contextpack/v1",
  "goal": "Let GrokBot use bat to inspect text files",
  "source": { "kind": "github", "canonical": "github.com/sharkdp/bat" },
  "source_metadata": { "name": "bat", "version": "0.26.1" },
  "manifest_evidence": [
    { "finding": "cli_entrypoint", "confidence": "high",
      "evidence": ["package.json bin field declares command \"bat\""] }
  ],
  "capabilities": [ { "name": "cli.main", "kind": "cli" } ],
  "unresolved_questions": ["which authentication does bat require?"],
  "constraints": ["GrokBot receives a compact contract, never the repository contents."]
}
```

Exactly six things: goal, source metadata, evidence, capabilities, unresolved
questions, constraints. No source files. No documentation corpus.

## Bounds

| Bound | Value |
| --- | --- |
| metadata value length | 512 bytes |
| pack sections | the six above, enforced at marshal time |
| evidence entries per finding | 8, with a count of what was omitted |

Bounds are enforced at serialization, so a pack cannot smuggle content even if
it was built without the fluent helpers.

## Redaction

Secret-shaped text is redacted from every field before it enters a pack. A
Context Pack may say "README documents installation and configuration" without
copying:

```text
export GITHUB_TOKEN=ghp_...
```

Redaction preserves the variable name so a worker still knows what it is looking
at, and it is idempotent: a sanitized string can be re-inspected safely.

## When a worker is invoked

GrokInstall invokes an external reasoning or coding worker only when a
deterministic path is insufficient — for example, generating an adapter for a
surface with no existing bridge.

It does not send a Context Pack when no reasoning is required. In the verified
`bat` provisioning path, zero worker calls were made: the release route was
deterministic throughout.

A future builder integration should hand OpenCode or Cursor a **local checkout
to inspect directly** when that is legitimate, rather than serializing a
repository through GrokBot.

## Future worker types

The worker protocol is provider-agnostic and already defined. OpenCode, Cursor,
ChatGPT, Claude, Grok and local scripts are **recognized optional
configurations, not integrated providers**. Their absence is reported honestly
rather than silently ignored.

See [ROADMAP.md](../ROADMAP.md) for when builder integrations are expected.
