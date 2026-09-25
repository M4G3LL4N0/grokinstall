# Example: a GitHub CLI project

Demonstrates the full path from a public repository to a runnable capability.

**Input**

```text
SOURCE: https://github.com/sharkdp/bat
GOAL:   "Let GrokBot use bat to inspect text files"
```

**Command**

```bash
grokinstall install https://github.com/sharkdp/bat \
  --goal "Let GrokBot use bat to inspect text files"
```

**Decision**

`bat` ships a CLI capability and no suitable executable was present, so
provisioning is required. The release artifact is the smallest safe route.

**Result**

```text
Strategy:   cli_bridge (supported)
Runtime:    ~/.grokinstall/runtimes/bat.search (GrokInstall-owned)
method:     github_release
version:    v0.26.1
asset:      bat-v0.26.1-aarch64-apple-darwin.tar.gz
checksum:   unavailable upstream
note:       upstream publishes no checksum: the artifact was hashed but not
            verified against a published value
```

**Why this worked**

`bat` publishes release binaries for common platforms, so the route is
deterministic and verifiable. Nothing was installed on the system `PATH`, and
no install script ran.

**This is not a claim about every repository.** A project with no release
artifact, or one that requires a build backend, takes a different path — see
[`../unsafe-python`](../unsafe-python).

**Verify**

```bash
grokinstall capabilities
grokininstall test bat.search
"$HOME/.grokinstall/runtimes/bat.search/bin/bat" --version
grokinstall audit bat.search
grokinstall uninstall bat.search
```
