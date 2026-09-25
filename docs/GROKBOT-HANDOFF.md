# GrokBot handoff

This is the integration handbook for wiring GrokBot to GrokInstall.

## The entire contract

```bash
grokinstall capabilities --json     # what can I call?
grokinstall grokbot NAME --json     # how do I call it?
grokinstall run NAME --input '{}'  # call it
```

On failure:

```bash
grokinstall diagnose NAME --json
```

That is the whole interface. GrokBot does **not** need the README, the
repository tree, package manifests, source code, or GrokInstall's strategy
internals to use a capability. Those are only relevant when a user is actively
debugging a GrokInstall installation.

## 1. Discover

```bash
$ grokinstall capabilities --json
{
  "capabilities": [
    {
      "name": "bat.view",
      "description": "the user wants GrokBot to use bat to inspect text files",
      "strategy": "cli_bridge",
      "support": "supported",
      "state": "ready",
      "runnable": true,
      "call": "grokinstall run bat.view --input '<json>'",
      "input":  { "fields": [] },
      "output": { "fields": [] },
      "grokbot_contract": "CAPABILITY\nbat.view\n..."
    }
  ],
  "total": 1,
  "shown": 1
}
```

**Only runnable capabilities appear by default.** A capability in `broken` or
`dirty` state is never offered as callable. If you need to see everything for
diagnostics, use `--all` and check `runnable` yourself.

A capability name is stable. Its manifest path, runtime path and receipt are
implementation details GrokBot should not depend on.

## 2. Read the contract

```bash
$ grokinstall grokbot bat.view
```

```text
CAPABILITY
bat.view

USE WHEN
the user wants GrokBot to: Let GrokBot use bat to review text/source files

CALL
grokinstall run bat.view --input '<json>'

INPUT
style: string - passed as --style <value>
language: string - passed as --language <value>
path: string (required) - passed as argument 1 to the tool

OUTPUT
text: string - the tool's own output, verbatim

OPERATION
view (translated to this tool's own arguments)

NOTES
bat is a file display tool; this capability displays a file, it does not review code

DO NOT
load the implementation repository or its documentation into GrokBot before
invoking this capability

ON FAILURE
Run:
grokinstall diagnose bat.view
```

The INPUT and OUTPUT sections are derived from the capability's invocation
mapping, not written by hand. Each field says where its value actually goes,
and the OUTPUT section names the mode the upstream tool really produces. A
capability that prints text says so; it does not promise a JSON result the tool
never emits.

The contract is bounded: 8192 bytes hard cap, 670 bytes measured for the
flagship `bat` run on a long goal. It contains only capability, when to use it,
how to call it, input, output, the operation, limitations and failure recovery.
It never contains secrets, local paths, README instructions, receipt internals or
source snippets.

## 3. Call

```bash
grokinstall run bat.view --input '{"path":"/path/to/file.go"}'
```

Input is one JSON object, via `--input` or stdin. Output is one envelope:

```json
{ "ok": true, "result": {}, "artifacts": [], "warnings": [] }
```

Check `ok`. A failure is structured, never a crash:

```json
{
  "ok": false,
  "error": {
    "code": "timeout",
    "message": "capability timed out after 30s"
  }
}
```

| Code | Meaning |
| --- | --- |
| `invalid_input` | the input was not a JSON object |
| `not_executable` | the capability is a plan, not a runnable capability |
| `spawn_failed` | the executable could not be started |
| `timeout` | the capability exceeded its timeout |
| `output_too_large` | output exceeded the declared bound |
| `nonzero_exit` | the command failed; stderr is included |
| `malformed_output` | the command promised JSON and did not deliver it |
| `handler_failed` | an internal handler failed |

## 4. Recover

```bash
grokinstall diagnose NAME --json
```

Each finding gives a symptom, the evidence observed, the root cause, a
confidence, the affected component, the smallest fix, and a verification
command. Forward the `smallest_fix` to the user; do not improvise a repair.

```json
{
  "reports": [{
    "capability": "bat.view",
    "healthy": false,
    "findings": [{
      "severity": "critical",
      "symptom": "provisioned runtime was modified after installation",
      "evidence": ["runtime: ~/.grokinstall/runtimes/bat.view", "bin/bat: content changed"],
      "root_cause": "files no longer match the hashes recorded at provisioning time",
      "confidence": "high",
      "component": "runtime",
      "smallest_fix": "reinstall the capability to restore a verified runtime",
      "verification_command": "grokinstall audit bat.view"
    }]
  }]
}
```

## Operating rules for GrokBot

1. **Never load the repository.** The contract is the interface. If a capability
   does not expose what the user needs, say so and suggest an install goal.
2. **Trust `runnable`, not existence.** A listed capability may be broken; the
   default capability list already filters this.
3. **Do not re-implement a capability.** If `bat.view` exists, call it.
4. **Forward failures with their fix.** `diagnose` is the answer to "why is this
   broken"; do not guess.
5. **Pass input through, do not transform it.** The capability owns its own
   interface.
6. **Expect bounded output.** Capabilities are bounded by design; a capability
   returning an enormous payload is a bug to report, not context to absorb.
7. **Do not re-run inspection to answer a runtime question.** `run` and
   `diagnose` are the runtime path; `inspect` is for installation decisions.

## What GrokBot should never need

- the upstream repository's README
- its file tree
- its package manifests
- its source code
- GrokInstall's strategy comparison internals
- a provisioner's configuration

These exist for a human debugging GrokInstall. They are not the interface.
