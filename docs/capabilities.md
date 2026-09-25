# Capabilities

A capability is something GrokBot can call. It exists only when an inspected
artifact backs it.

## Installed capabilities

```bash
grokinstall list
```

```text
NAME        STRATEGY    SUPPORT    STATE    STATUS
bat.view  cli_bridge  supported  ready    ready
```

## Runnable capabilities

```bash
grokinstall capabilities
```

```text
bat.view  [ready]
  the user wants GrokBot to use bat to inspect text files
  call: grokinstall run bat.view --input '<json>'
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
  "name": "bat.view",
  "version": "1",
  "source": "github.com/sharkdp/bat",
  "goal": "Let GrokBot use bat to inspect text files",
  "strategy": "cli_bridge",
  "support": "supported",
  "state": "ready",
  "execution": {
    "type": "subprocess",
    "supported": true,
    "command": "/Users/you/.grokinstall/runtimes/bat.view/bin/bat",
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
grokinstall run bat.view --input '{"query":"TODO"}'
echo '{"query":"TODO"}' | grokinstall run bat.view
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

## How input becomes a real command

Since v0.2 a `cli_bridge` capability is an **adapter**, not a registered binary.
The manifest records a mapping, and that mapping is what turns structured input
into a real invocation:

```json
{
  "adapter": {
    "kind": "cli",
    "output_mode": "text",
    "output_field": "text",
    "operation": "view",
    "confidence": "high",
    "layer": "known_adapter",
    "evidence": ["bat takes a FILE argument and prints its contents"],
    "argv": [
      {"from": "style", "kind": "flag", "flag": "--style"},
      {"from": "path", "kind": "positional", "position": 0, "required": true}
    ]
  }
}
```

Supported binding kinds:

| kind | meaning |
| --- | --- |
| `positional` | a bare argument at a declared index |
| `flag` | `--flag value` as two separate arguments |
| `flag_equals` | `--flag=value` as one argument |
| `boolean_flag` | the flag when true, nothing when false |
| `repeated_flag` | the flag once per array item |
| `stdin` | the value written to standard input |
| `literal` | a constant argument such as a subcommand |
| `working_dir` | the child's working directory |

There is deliberately **no** `shell_template` kind. Every execution is a direct
`execve` with a separate argument vector: no shell, no `sh -c`, no interpolation
into a command line. A value that begins with `-` is refused in a positional slot
unless the mapping opts in, so a caller cannot smuggle a flag into a path.

## Where the mapping comes from

Discovery is layered, and each layer is trusted less than it would like to be:

| layer | evidence | confidence |
| --- | --- | --- |
| A | package manifests, declared entrypoints, machine-readable CLI metadata | medium |
| B | a bounded `tool --help` under a timeout, output bound and minimal environment | medium |
| C | an explicit known adapter for a tool with an established mapping | high |
| D | nothing trustworthy was found | `adapter_required` |

Layer B help output is treated strictly as **untrusted data**. It is parsed only
for a declared file/path positional and the presence of long flags. Embedded
commands, URLs and instructions in help text are never followed and never become
part of a mapping.

When discovery reaches layer D, GrokInstall returns `adapter_required` and the
capability stays **plan-only**. A capability that cannot honestly be mapped is
better than one that runs and does nothing useful.

## Naming follows the evidence

A capability name is a promise. A tool that only displays files is not published
as `tool.review` just because the goal asked for a review; it is published as
`tool.view`. When no operation can be evidenced, the name falls back to a neutral
`tool.run` rather than inventing semantics.

## Ownership and integrity

`execution.ownership` records who controls the executable, and that decides
whether integrity is a hard execution gate:

| ownership | meaning | integrity |
| --- | --- | --- |
| `grokinstall` | GrokInstall provisioned it into a runtime it controls | hash-enforced; a change blocks execution |
| `external` | the executable already existed on the machine | not enforced; a package upgrade is normal |
| `user_supplied` | the user pointed GrokInstall at an executable | not enforced; the user manages it |

The asymmetry is deliberate. GrokInstall can only promise the bytes it installed,
so only those are checked. Reporting a routine `brew upgrade` as tampering would
make the tool cry wolf and train people to ignore it.
