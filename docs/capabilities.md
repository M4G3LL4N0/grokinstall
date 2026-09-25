# Capabilities

A capability is something GrokBot can call. It exists only when an inspected
artifact backs it.

## Installed capabilities

```bash
grokinstall list
```

```text
NAME        STRATEGY    SUPPORT    STATE    STATUS
bat.review  cli_bridge  supported  ready    ready
```

## Runnable capabilities

```bash
grokinstall capabilities
```

```text
bat.review  [ready]
  the user wants GrokBot to use bat to inspect text files
  call: grokinstall run bat.review --input '<json>'
```

By default this shows only **runnable** capabilities, so GrokBot is never handed
something it cannot call.

```bash
grokinstall capabilities --json     # machine-readable
grokinstall capabilities --all      # include broken, dirty, non-runnable
grokinstall capabilities --state=broken
```

## Lifecycle states

| State | Meaning | Runnable |
| --- | --- | --- |
| `ready` | installed, verified, working | yes |
| `broken` | installed but no longer working | no |
| `dirty` | a mutation partially applied; rollback could not be proven | no |
| `uninstalled` | removed, receipt retained | no |
| `plan_only` | a persisted plan, never a capability | no |

A plan is not an installed capability. Plan-only strategies live in
`~/.grokinstall/plans/`, outside the capability registry.

## The manifest

```json
{
  "schema": "grokinstall/v1",
  "name": "bat.review",
  "version": "1",
  "source": "github.com/sharkdp/bat",
  "goal": "Let GrokBot use bat to inspect text files",
  "strategy": "cli_bridge",
  "support": "supported",
  "state": "ready",
  "execution": {
    "type": "subprocess",
    "supported": true,
    "command": "/Users/you/.grokinstall/runtimes/bat.review/bin/bat",
    "input_mode": "stdin_json",
    "timeout_ms": 30000,
    "max_output_bytes": 1048576
  },
  "input":  { "fields": [] },
  "output": { "fields": [] },
  "grokbot":   { "use_when": "...", "do_not": "...", "on_failure": "..." },
  "cache":     { "enabled": false },
  "security":  { "executes_source_code": true, "owned_by_grokinstall": true },
  "provenance": { "source": "...", "commit_sha": "...", "install_id": "..." }
}
```

A manifest may never claim `execution.supported` unless it passed verification.
It contains no source code, no file listing and no documentation text.

## Input and output

Input arrives as one JSON object:

```bash
grokinstall run bat.review --input '{"query":"TODO"}'
echo '{"query":"TODO"}' | grokinstall run bat.review
```

Output uses one envelope:

```json
{ "ok": true, "result": {}, "artifacts": [], "warnings": [] }
```

Failures are structured, not crashes:

```json
{
  "ok": false,
  "error": { "code": "timeout", "message": "capability timed out after 30s" }
}
```

Codes include `invalid_input`, `not_executable`, `spawn_failed`, `timeout`,
`output_too_large`, `nonzero_exit`, `malformed_output` and `handler_failed`.

## Knowledge capabilities

Documentation sources become a bounded local index, not a copy of the docs in
model context.

```bash
grokinstall install ./docs --goal "Let GrokBot retrieve relevant documentation"
grokinstall run project.docs --input '{"query":"authentication","limit":3}'
```

```json
{
  "query": "authentication",
  "matches": [
    { "file": "docs/auth.md", "title": "Authentication", "excerpt": "...", "score": 12.4 }
  ],
  "total_documents": 64
}
```

Indexing is bounded by file count, per-file size and total size. Binary content
is rejected. Excerpts are bounded. There are no embeddings and no vector
database: the ranking is a plain term-frequency score you can explain.
