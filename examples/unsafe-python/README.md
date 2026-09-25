# Example: a safe refusal

Demonstrates that GrokInstall is willing to stop, explain and change nothing.

**Input**

```text
SOURCE: https://github.com/pallets/click
GOAL:   "Let GrokBot use click's CLI capability"
```

**Command**

```bash
grokinstall install https://github.com/pallets/click \
  --goal "Let GrokBot use click's CLI capability"
```

**What GrokInstall found**

A Python project whose CLI would be obtained through a package manager. There
are no release binaries, so the only provisioning route is a Python install —
which may execute the project's build backend.

**Decision: blocked under the safe policy**

```text
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

**Result**

| Check | State |
| --- | --- |
| registered capabilities | none |
| installed anything | no |
| changed system state | no |
| plan persisted outside the registry | yes, for audit |
| staging residue | none |

**Your options**

```bash
# Deliberately allow a specific risk class
grokinstall install https://github.com/pallets/click \
  --goal "..." --allow-install-scripts

# Or avoid it entirely and use a documentation capability
grokinstall install https://github.com/pallets/click \
  --goal "Let GrokBot retrieve relevant documentation about click"

# Or bridge an executable you already trust
grokinstall install https://github.com/pallets/click \
  --goal "..." --provision=never
```

**Why this is a good outcome**

A tool that stops, explains the exact blocker, names the specific authorization
that would unlock it, registers nothing, and changes nothing is more useful than
one that quietly runs a build backend as your user.
