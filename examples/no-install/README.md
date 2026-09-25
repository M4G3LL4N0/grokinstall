# Example: when nothing needs installing

Demonstrates that "do not install" is a first-class successful answer.

**Input**

A directory containing only prose — a README with no interface, no CLI, no API.

**Command**

```bash
grokinstall install ./notes --goal "summarize the notes for users"
```

**Decision**

No callable surface was detected. Rather than manufacturing a capability, the
recommendation is `no_install`.

**Result**

```text
Result: no_install
Why:    Nothing needs installation for this goal: only documentation was
        detected, and the goal does not require retrieving it
```

A receipt is still written, recording the decision and its reason.

**After**

```bash
grokinstall capabilities
# No runnable capabilities installed.
```

Nothing is registered. The registry is untouched. There is no fake executable
and no manifest pretending something exists.

**Why this matters**

The most common integration mistake is installing an entire application to use
three functions. GrokInstall is built to notice when the honest answer is "you
already have what you need, or you need less than you think".
