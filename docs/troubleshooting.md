# Troubleshooting

## `doctor` first

```bash
grokinstall doctor
```

It reports CRITICAL, ACTION REQUIRED and OK sections, with a fix for anything
actionable. Missing optional tools are never failures.

## A capability will not run

```bash
grokinstall diagnose NAME
```

Common causes:

### "execution target missing"

The executable was removed or the manifest points somewhere stale.

```bash
grokinstall install SOURCE --goal "..." --replace
```

### "provisioned runtime was modified after installation"

Something changed the file in `~/.grokinstall/runtimes/`. That is an integrity
failure and `diagnose` reports it as critical.

```bash
grokinstall audit NAME
grokinstall install SOURCE --goal "..." --replace
```

### "capability is broken"

`diagnose` names the recorded reason. Fix the cause, then:

```bash
grokinstall test NAME
```

## Installation is blocked

```text
PROVISIONING BLOCKED
```

GrokInstall found no route that fits the safe policy. Read the required
authorization and decide deliberately:

```bash
# allow a specific risk class
grokinstall install SOURCE --goal "..." --allow-install-scripts

# or avoid provisioning entirely
grokinstall install SOURCE --goal "..." --provision=never

# or point at an executable you already trust
grokinstall install SOURCE --goal "..." --command /path/to/tool
```

Never reach for a generic bypass. There isn't one, deliberately.

## Nothing is registered after install

Check the outcome:

```bash
grokinstall install SOURCE --goal "..." --dry-run
```

Possible results:

| Result | Meaning |
| --- | --- |
| `installed` | a capability was registered and verified |
| `planned` | a plan-only strategy; a plan was saved, nothing registered |
| `no_install` | nothing needed installing; this is a success |
| `dry_run` | nothing was changed |
| `failed` | verification or commit failed; nothing registered |

## Public GitHub inspection fails

```bash
grokinstall doctor
```

GitHub inspection needs `git`. Check network access, and that the repository is
public.

## A capability times out

The timeout is part of the manifest. Reinstall with a longer one:

```bash
grokinstall install SOURCE --goal "..." --timeout-ms 120000
```

## Public repositories fail in CI

Live network tests are opt-in so CI is not flaky:

```bash
GROKINSTALL_NETWORK_TESTS=1 go test ./internal/inspect/ -run PublicGitHub -v
```

## Still stuck

Open an issue with:

```bash
grokinstall version
grokinstall doctor
grokinstall diagnose NAME --json
grokinstall audit NAME --json
```
