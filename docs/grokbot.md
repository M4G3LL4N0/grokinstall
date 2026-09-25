# GrokBot integration

GrokBot needs three commands. It does not need the repository.

```bash
grokinstall capabilities --json     # what can I call?
grokinstall grokbot NAME --json     # how do I call it?
grokinstall run NAME --input '{}'  # call it
```

On failure:

```bash
grokinstall diagnose NAME --json
```

## Discovery

```bash
$ grokinstall capabilities --json
{
  "capabilities": [
    {
      "name": "bat.review",
      "description": "the user wants GrokBot to use bat to inspect text files",
      "strategy": "cli_bridge",
      "support": "supported",
      "state": "ready",
      "runnable": true,
      "call": "grokinstall run bat.review --input '<json>'",
      "input":  { "fields": [] },
      "output": { "fields": [] }
    }
  ],
  "total": 1,
  "shown": 1
}
```

Only runnable capabilities appear by default. A broken or dirty capability is
never offered as callable.

## The contract

```bash
$ grokinstall grokbot bat.review
```

```text
CAPABILITY
bat.review

USE WHEN
the user wants GrokBot to: Let GrokBot use bat to inspect text files

CALL
grokinstall run bat.review --input '<json>'

INPUT
input: object - JSON object passed to the capability on stdin

OUTPUT
result: object - the capability's JSON result

DO NOT
load the implementation repository or its documentation into GrokBot before
invoking this capability

ON FAILURE
Run:
grokinstall diagnose bat.review
```

The measured contract for the flagship `bat` run was **432 bytes**. The hard
cap is 8192; field lists are capped too.

The contract contains only: capability, when to use it, how to call it, input,
output, limitations, and failure recovery. It never contains secrets, local
source paths, README instructions, receipt internals or source snippets.

## Calling

```bash
grokinstall run bat.review --input '{"query":"TODO"}'
echo '{"query":"TODO"}' | grokinstall run bat.review
```

```json
{ "ok": true, "result": {}, "artifacts": [], "warnings": [] }
```

## Failure

```bash
grokinstall diagnose bat.review --json
```

```json
{
  "reports": [
    {
      "capability": "bat.review",
      "healthy": false,
      "findings": [
        {
          "severity": "critical",
          "symptom": "provisioned runtime was modified after installation",
          "evidence": ["runtime: ~/.grokinstall/runtimes/bat.review", "bin/bat: content changed"],
          "root_cause": "files no longer match the hashes recorded at provisioning time",
          "confidence": "high",
          "component": "runtime",
          "smallest_fix": "reinstall the capability to restore a verified runtime",
          "verification_command": "grokinstall audit bat.review"
        }
      ]
    }
  ]
}
```

## What GrokBot does not need

For normal operation, GrokBot does not need the README, the repository tree,
package manifests, source code, or GrokInstall's strategy internals. Those are
needed only when a user is actively debugging a GrokInstall installation.

See [GROKBOT-HANDOFF.md](../docs/GROKBOT-HANDOFF.md) for the integration
handbook.
