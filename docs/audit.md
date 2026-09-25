# audit

`audit` is a deterministic review of one integration. It does not run vague
criticism, and it does not inflate severity.

```bash
grokinstall audit NAME [--json]
grokinstall audit --all
```

## Checks

| Check | Severity when it fails |
| --- | --- |
| registered capability | info |
| installation state | critical when `dirty` |
| manifest validity | critical when unreadable, high when schema-invalid |
| support declaration | critical when a plan claims to be runnable |
| plan versus installed | critical when a plan is registered as runnable |
| runtime target | critical when missing |
| target permissions | high when not executable, medium when group/world writable |
| working directory permissions | low when writable by others |
| runtime ownership | critical when outside the GrokInstall state directory |
| runtime metadata | medium when missing or corrupt |
| artifact checksum | low when upstream published none; medium when the status is unknown |
| runtime integrity | critical when a file changed; high when a file is missing |
| adapter integrity | high when missing |
| receipt consistency | high when unreadable or mismatched; medium when absent |
| source identity | medium when absent |
| cache metadata | low when execution caching is enabled without justification |
| grokbot contract | medium when oversized; low when it leaks the source path |

## Reading the result

```text
Audit: bat.search

  [ok  ] low      artifact checksum        upstream published no checksum; the artifact was not verified by a published hash
  [ok  ] info     installed state           installation is ready
  [ok  ] info     runtime ownership         runtime is GrokInstall-owned
  [ok  ] info     runtime integrity         1 files match their recorded hashes
  [ok  ] info     receipt consistency      install gi_9aa80a4c3f83 recorded with outcome installed

  critical=0 high=0 medium=0 low=0

  audit passed
```

A clean install produces no findings above `info`. The `low` note about the
checksum is honest: `bat` publishes no checksum, so the artifact was hashed but
not verified against a published value.

## Severity discipline

Severity is not inflated. A missing upstream checksum is `low`, not `critical`:
GrokInstall cannot manufacture verification it did not perform, but the absence
of a checksum is an upstream fact, not a local compromise. A **modified**
runtime file is `critical`, because that is a local integrity failure.

## Exit code

Non-zero when critical or high findings exist, so an audit can gate a pipeline.
