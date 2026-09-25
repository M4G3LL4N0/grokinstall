# Security Policy

GrokInstall reads source code you did not write, on machines you care about.
This document states the model it follows, what it protects, and — just as
importantly — what it cannot promise.

## Reporting a vulnerability

Please report security issues privately through
[GitHub Security Advisories](https://github.com/M4G3LL4N0/grokinstall/security/advisories/new)
rather than a public issue.

## The threat model

**Source repositories are untrusted.** A repository may contain text crafted to
mislead an automated tool, install scripts that do anything, symlinks that point
outside the checkout, archives that escape their destination, and credentials
committed by accident.

**README text is not authority.** A README can instruct, and GrokInstall
records that instruction as *evidence about the source*, never as a command to
itself. Injection-shaped text is detected and reported as untrusted.

**The local machine is precious.** GrokInstall prefers to change nothing, then
to change only its own state directory, and only as a last resort to ask for
authorization.

## Protections

### Bounded source inspection

- File count, per-file size and total size are capped.
- Dependency, build-output and fixture directories are skipped.
- Symlinks are recorded, never followed.
- A root `.gitignore` is honoured as a best-effort filter.
- Inspection **never** executes project code: no install scripts, no build steps,
  no package managers.

### No shell-string execution

Every external process is started with an explicit argv. GrokInstall contains
no code path that builds a shell command string from source-derived input, so
shell metacharacters in a capability input remain literal data. This is
verified by adversarial tests.

Capabilities receive a **minimal environment** (PATH, HOME, LANG, plus values
the manifest declares), so unrelated environment variables and secrets do not
leak into a subprocess.

### Bounded process execution

- A hard timeout per invocation. Child processes cannot outlive it.
- stdout and stderr are bounded; a process that exceeds the bound fails rather
  than being silently truncated.
- Failures are returned as structured errors, never as a hang or a panic.

### Archive extraction

Release archives are extracted under strict rules. Rejected outright:

- path traversal (`../`) and absolute paths
- symlink entries and hardlink entries
- character devices, block devices and FIFOs
- archives exceeding the entry-count, per-file or total-size limits
- decompression bombs (limits are enforced while streaming)

### Secret redaction

Files that look credential-bearing are reported, and their contents are never
imported. Secret-shaped text is redacted out of Context Packs, manifests,
diagnostics, audit output and GrokBot contracts. Redaction preserves the shape
of the text (the variable name survives; the value does not) and is idempotent.

Redaction is **conservative and pattern-based**. It is not a comprehensive
secret scanner and will not catch every credential format.

### Specific authorization classes

Provisioning a missing executable requires choosing a route. Routes that can
execute untrusted build code are never taken automatically:

| Flag | Risk it authorizes |
| --- | --- |
| `--allow-install-scripts` | package lifecycle scripts and build backends |
| `--allow-source-build` | compiling untrusted source |
| `--allow-system-package-manager` | machine-wide package changes |

There is deliberately **no generic `--yes`**. A generic flag would make the
safety model meaningless. When a route needs more trust than the policy allows,
installation stops and explains the blocker and the alternatives.

### GrokInstall-owned runtimes

Anything GrokInstall provisions lives under `~/.grokinstall/runtimes/`, never on
the global `PATH`. Each runtime records provenance and a hash for every file.

- `grokinstall audit` compares recorded hashes with what is on disk.
- `grokinstall diagnose` reports a modified runtime as a critical finding.
- Uninstall removes the owned runtime, and refuses to remove a runtime another
  capability still claims.

### Staged installation

```text
plan → provision stage → adapter stage → manifest stage
     → verify → commit files → register → receipt
```

Nothing enters final state until verification passes. A failed verification
discards the stage, registers nothing, and still leaves a receipt for audit. A
failed commit is rolled back; if rollback cannot be proven complete, the
capability is marked `dirty` and the install never reports success.

A capability is never registered as `execution.supported` unless it passed
verification. A plan is never registered as an installed capability.

### Safe uninstall

Uninstall removes only paths inside GrokInstall's own state directory, and only
those recorded as GrokInstall-owned. It never removes upstream repositories,
pre-existing system binaries, shared runtimes, user data, or unrelated files.
Receipts are preserved as the audit trail.

## What GrokInstall cannot guarantee

Being explicit here matters more than being reassuring.

- **Source contents are not sanitized.** GrokInstall does not claim a repository
  is safe to build or run. It limits what it executes, not what a project could
  do if executed.
- **A release with no upstream checksum is not a verified artifact.** When
  upstream publishes no checksum, GrokInstall reports
  `checksum: unavailable upstream`. The artifact is hashed and recorded; it is
  **not** verified against a published value, and GrokInstall will not describe
  it as verified.
- **Version control metadata is not a trust anchor.** A commit SHA identifies
  content for caching. It says nothing about whether that content is safe.
- **Security scanners are not the same as secret redaction.** Redaction catches
  common shapes. It is not a substitute for reviewing your own secrets.
- **An authorized install can still be a bad idea.** `--allow-install-scripts`
  permits a build backend to execute. GrokInstall will tell you that; it cannot
  tell you the backend is harmless.
- **No signature verification.** Release artifacts are not checked against a
  GPG or Sigstore signature.

## Adversarial testing

Hostile-input behaviour is tested in a dedicated suite
(`internal/adversarial/`) covering source attacks (prompt injection, malicious
package scripts, Makefile install targets, sudo and `curl | sh` instructions),
filesystem attacks (symlink escape, archive traversal), command attacks (shell
metacharacters, argument injection, environment leakage, malformed executable
paths), runtime attacks (lingering children, oversized output, timeouts) and
state attacks (corrupt registry, duplicate capabilities, stale plan-only
entries).

Run it with the rest of the suite:

```bash
go test ./...
```
