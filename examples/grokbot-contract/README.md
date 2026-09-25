# Example: the GrokBot contract

The contract is the interface. This directory shows what GrokBot actually
receives for a real capability.

**Produce it**

```bash
grokinstall grokbot bat.review
```

**Measured output for the flagship `bat` run: 432 bytes.**

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

**What it contains**

Only: capability, when to use it, how to call it, input, output, limitations and
failure recovery.

**What it never contains**

- secrets or credential material
- local source paths
- README instructions or documentation text
- receipt internals
- source snippets
- provider credentials

**The GrokBot loop**

```bash
grokinstall capabilities --json     # what can I call?
grokinstall grokbot NAME --json     # how do I call it?
grokinstall run NAME --input '{}'  # call it
```

Failure:

```bash
grokinstall diagnose NAME --json
```

**Why the size matters**

The point of the contract is that GrokBot's footprint is bounded by the
capability, not by the repository. A 432-byte contract replacing an entire
project is the product working as intended.

The exact number varies with the capability's name, goal and input schema. The
hard cap is 8192 bytes; the flagship measurement is reported here as observed, not
as a promise about every capability.
