---
name: Bug report
about: Something behaves incorrectly
title: "[bug] "
labels: bug
assignees: ''
---

## What happened

<!-- A clear, factual description of the incorrect behavior. -->

## What you expected

## Reproduction

```bash
# The exact commands, including flags, that reproduce it.
```

If a source is involved, please say whether it is local or a public
repository, and include the repository URL if public.

## Environment

```bash
grokinstall version
```

Please paste the full output, including the commit and platform. A development
build is fine; say so if it is one.

```text
grokinstall version output:
OS/arch:
Go version (if built from source):
```

## Relevant state

Only if it does not contain anything sensitive. Otherwise describe it.

- `grokinstall doctor` output
- `grokinstall diagnose NAME --json` output
- `grokinstall audit NAME --json` output

## Anything else

<!-- Logs, error text, or what you already tried. -->
