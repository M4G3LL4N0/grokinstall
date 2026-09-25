# diagnose

`diagnose` answers "why is this not working?" with evidence rather than
speculation.

```bash
grokinstall diagnose NAME [--json]
grokinstall diagnose --all
```

## Output

Every finding carries seven fields:

```text
[CRITICAL]
SYMPTOM
  bat.search cannot execute

EVIDENCE
  manifest target: ~/.grokinstall/runtimes/bat.search/bin/bat
  filesystem: target missing

ROOT CAUSE
  the executable is absent at the registered path

CONFIDENCE
  high

AFFECTED COMPONENT
  execution

FIX
  reinstall the capability to re-provision the executable

VERIFY
  grokinstall test bat.search
```

`CONFIDENCE` is earned. Direct filesystem evidence is `high`; a weak signal is
`low`. GrokInstall does not present a guess as a conclusion.

## What it checks

| Area | Checks |
| --- | --- |
| lifecycle | `dirty` state, `broken` state, with the recorded reason |
| manifest | missing, unreadable, corrupt, schema-invalid |
| execution | target missing, target is a directory, not executable |
| adapter | missing adapter directory |
| runtime | missing runtime, missing or corrupt metadata, metadata with no executable, **files modified since provisioning** |
| receipt | unreadable receipt, receipt naming a different capability, missing receipt |

## Ordering

Findings are ordered by severity: critical, high, medium, low, info. The first
finding is usually the thing to fix.

## In automation

```bash
grokinstall diagnose bat.search --json
```

```json
{
  "reports": [
    {
      "capability": "bat.search",
      "healthy": false,
      "findings": [
        {
          "severity": "critical",
          "symptom": "provisioned runtime was modified after installation",
          "evidence": ["runtime: ~/.grokinstall/runtimes/bat.search", "bin/bat: content changed"],
          "root_cause": "files no longer match the hashes recorded at provisioning time",
          "confidence": "high",
          "component": "runtime",
          "smallest_fix": "reinstall the capability to restore a verified runtime",
          "verification_command": "grokinstall audit bat.search"
        }
      ]
    }
  ]
}
```

Exit code is non-zero when a problem is found, so a script can branch on it.

## diagnose versus audit

| | diagnose | audit |
| --- | --- | --- |
| Question | why is it broken? | is this integration sound? |
| Focus | failure and recovery | configuration and provenance |
| Output | findings with fixes | pass/fail checks by severity |

`diagnose` is for a broken capability. `audit` is for reviewing a working one.
