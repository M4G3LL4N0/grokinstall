# Provisioning

A capability detected but not runnable is the normal case, not a failure. The
hard part is obtaining an executable without becoming a package manager.

```bash
grokinstall install SOURCE --goal "..." [--provision=safe|never|prompt]
```

## The order of preference

| # | Route | Changes | Safe by default |
| --- | --- | --- | --- |
| 1 | already-installed compatible executable | nothing | yes |
| 2 | verifiable upstream release artifact | owned runtime only | yes |
| 3 | reproducible build into an owned runtime | owned runtime only | yes (`go build` only) |
| 4 | language package manager | isolated or global, may run build code | no |
| 5 | system package manager | machine-wide | no |
| 6 | user-provided command | nothing | explicit |
| 7 | unresolved | nothing | reported, not guessed |

These are not equally safe. A release artifact downloaded into a private
directory is a different proposition from an npm install that may run
`postinstall` as your user.

## Policy and authorization

```bash
--provision=safe      # default: routes needing no extra authorization
--provision=never     # never provision; explain what would have been possible
--provision=prompt    # behave like safe, surface decisions to the caller
```

Approvals name a risk class:

| Flag | Unlocks |
| --- | --- |
| `--allow-install-scripts` | package lifecycle scripts and build backends |
| `--allow-source-build` | compiling untrusted source |
| `--allow-system-package-manager` | machine-wide package changes |

There is deliberately **no generic `--yes`**. A generic flag would make the
policy meaningless. When a route needs more trust than the policy allows,
installation stops and says exactly what is blocked, what the risk is, which
authorization would unlock it, and what the alternatives are.

## Owned runtimes

Anything GrokInstall provisions lives here:

```text
~/.grokinstall/runtimes/<capability>/
├── bin/<tool>
└── metadata.json
```

- no global `PATH` pollution
- version isolation
- clean uninstall
- explicit ownership
- per-file hashes for integrity

`metadata.json` records the method, source, version, asset, checksum status,
per-file hashes, ownership and any approvals. It is the reason `audit` can detect
modification and `diagnose` can explain it.

## GitHub release provisioning

1. resolve the current platform mechanically
2. fetch the latest release metadata
3. select a compatible asset conservatively — no match means no provisioning
4. download into staging, bounded in size
5. verify a published checksum **when upstream publishes one**
6. extract under strict archive rules
7. locate the executable, copy it into the owned runtime, hash it

When upstream publishes no checksum, the result says so:

```text
checksum: unavailable upstream
note:     upstream publishes no checksum: the artifact was hashed but not
          verified against a published value
```

GrokInstall does not describe an unverified artifact as verified.

## Go source builds

`go build`, and nothing else. No Makefile, no shell install script, no
`go generate`, no project-defined setup command. When a repository ships its own
build tooling, the provisioner refuses and says which authorization would be
needed, because routing around that would defeat the model.

## Worked example: a successful provision

```text
$ grokinstall install https://github.com/sharkdp/bat \
    --goal "Let GrokBot use bat to inspect text files"

Source:   github.com/sharkdp/bat
Strategy: cli_bridge (supported)
Runtime:  ~/.grokinstall/runtimes/bat.search (GrokInstall-owned)

Provisioning
  method:    github_release
  source:    github.com/sharkdp/bat
  version:   v0.26.1
  asset:     bat-v0.26.1-aarch64-apple-darwin.tar.gz
  checksum:  unavailable upstream
  note:      upstream publishes no checksum: the artifact was hashed but not
             verified against a published value

Verification passed
  [ok] manifest validates
  [ok] execution target exists
  [ok] execution target is executable
  [ok] capability launches
```

This worked because `bat` publishes a release artifact for this platform. It is
not a claim that every repository can be provisioned this way.

## Worked example: a safe refusal

```text
$ grokinstall install https://github.com/pallets/click \
    --goal "Let GrokBot use click's CLI"

PROVISIONING BLOCKED

Executable:
click

Safest available method:
package manager: python (uv/pip)

Risk:
  - Python installation may execute the project's build backend
  - may resolve and run project-defined packaging code
  - the package declares lifecycle scripts that would execute

Required authorization:
  --allow-install-scripts

Why:
the safest available method requires authorization beyond the safe policy

Alternatives:
  package_manager   contains lifecycle scripts that would execute
```

After the refusal: nothing installed, nothing registered, one plan persisted
outside the registry, no staging residue.

## Package managers

npm, Python and Cargo routes are **discovered and classified correctly**, but
have no executor. A Python install may run a build backend; an npm install may
run `postinstall`; a Homebrew formula changes machine-wide state. GrokInstall
reports these honestly rather than reporting an unimplemented route as working.
Executors are on the v0.2 roadmap.
