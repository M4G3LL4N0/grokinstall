# usage

`usage` reports what GrokInstall actually did, in measured terms.

```bash
grokinstall usage
grokinstall usage --json
```

## Counters

```text
Usage (measured)
  operations:         20
  installs:           9
  runs:               5
  tests:              1
  uninstalls:         1
  cache hits:         3
  cache misses:       8
  worker calls:       0
  context pack bytes: 6398
  contract bytes:     543
  capability input:   67 bytes
  capability output:  412 bytes
```

## Measurement discipline

- **Bytes are bytes.** A context pack measured at 2 KB is 2 KB. GrokInstall does
  not convert bytes to tokens, because that conversion depends on a tokenizer
  and would be a fabricated number. When a real tokenizer measurement is added,
  it will be labelled as such.
- **No invented savings.** There is no "saved 90% of context" figure, because
  nothing was measured in token terms.
- **Worker calls are counted, not assumed.** The verified `bat` provisioning
  path made zero worker calls; the counter reports zero because that is what
  happened.

## Where the data lives

```text
~/.grokinstall/logs/usage.jsonl
```

One JSON object per line, appended atomically. Corrupt lines are skipped rather
than failing the report, so a truncated write cannot hide history.

## Efficiency

The deterministic paths are the ones worth optimizing, and they are the ones
GrokInstall optimizes:

- inspection is cached by commit SHA or content hash, so re-planning an
  unchanged source does not re-read it
- capability **execution** results are never cached automatically; caching is a
  semantic decision, and a wrong default here is a correctness bug
- model calls happen only when a deterministic path is insufficient, and in the
  verified flagship path there were none
