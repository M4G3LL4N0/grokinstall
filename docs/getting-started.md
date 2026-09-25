# Getting started

GrokInstall takes a source and a goal, and gives GrokBot a small capability
instead of a repository.

## 1. Look before you install

Nothing changes when you inspect or compare.

```bash
grokinstall inspect https://github.com/sharkdp/bat
```

You get evidence-backed findings: entrypoints, manifests, tests, CI, licenses,
install scripts, and anything suspicious. Nothing is executed.

```bash
grokinstall compare https://github.com/sharkdp/bat \
  --goal "Let GrokBot use bat to inspect text files"
```

You get ten candidate strategies compared across twelve dimensions, and a
recommendation with the evidence behind it.

## 2. Install the capability

```bash
grokinstall install https://github.com/sharkdp/bat \
  --goal "Let GrokBot use bat to inspect text files"
```

If the capability needs an executable that is not present, GrokInstall
compares provisioning routes and takes the smallest safe one. If every route
needs more trust than the policy allows, it stops and explains the blocker
rather than proceeding.

Preview first if you prefer:

```bash
grokinstall install ./my-tool --goal "..." --dry-run
```

## 3. Give GrokBot the contract

```bash
grokinstall capabilities          # what can be called
grokinstall grokbot bat.search    # how to call it
```

## 4. Run it

```bash
grokinstall run bat.search --input '{}'
```

## When something breaks

```bash
grokinstall diagnose bat.search
grokinstall audit bat.search
grokinstall doctor
```

See [troubleshooting](troubleshooting.md).

## Where state lives

```text
~/.grokinstall/
├── config.json
├── registry.json     installed capabilities
├── manifests/        one contract per capability
├── adapters/         generated data, when a strategy needs it
├── receipts/         what each install changed
├── plans/            plans — deliberately not capabilities
├── runtimes/         GrokInstall-owned provisioned executables
├── staging/          work in progress, discarded on failure
├── cache/            deterministic inspection cache
├── diagnostics/
└── logs/usage.jsonl
```

Use `--state-dir` or `GROKINSTALL_HOME` to work somewhere else.
