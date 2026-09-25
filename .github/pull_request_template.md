# Pull request

## What this changes

<!-- What is different, and why. Link the issue if there is one. -->

## Which area

- [ ] `source` / `inspect` / `evidence` — understanding a source
- [ ] `capability` / `strategy` / `plan` — deciding what to do
- [ ] `provision` — obtaining an executable
- [ ] `runtime` / `knowledge` — executing a capability
- [ ] `installer` / `registry` / `cache` — the transaction and state
- [ ] `diagnose` / `audit` / `doctor` — explaining an installation
- [ ] `cli` — commands and output
- [ ] docs / CI / release

## Verification

- [ ] `go test ./...` passes
- [ ] `go vet ./...` clean
- [ ] `go test -race ./...` clean
- [ ] `go build ./...` succeeds
- [ ] `gofmt -l .` prints nothing
- [ ] adversarial tests pass (`go test ./internal/adversarial/`)

## Security review

If this touches a provisioner, a strategy, extraction, or command execution:

- [ ] Source content is still treated as hostile data
- [ ] No shell-string interpolation was introduced
- [ ] Bounds (size, count, time, output) are enforced
- [ ] New failure paths return structured errors
- [ ] Adversarial tests were added for the new behaviour
- [ ] Authorizations are specific; no generic bypass was added

If this is a new strategy or provisioner:

- [ ] It declares a support level honestly
- [ ] It appears in the README's Supported or Detected / Planned table
- [ ] Claim discipline was checked: nothing is presented as working that is not

## Claim discipline

- [ ] Any user-visible claim in docs matches verified behaviour
- [ ] "Detected" and "installed" are not conflated

## Documentation

- [ ] Updated `README.md`, `SECURITY.md` or the relevant `docs/` page
- [ ] Added a worked example if this introduces a new decision path
