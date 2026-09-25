# Contributing to GrokInstall

Thanks for considering a contribution. This document covers the setup, the
expectations, and the bar for new strategies and provisioners.

## Setup

GrokInstall requires **Go 1.24 or later**.

```bash
git clone https://github.com/M4G3LL4N0/grokinstall
cd grokinstall
go build -o grokinstall ./cmd/grokinstall
```

Some capabilities (public GitHub inspection, release provisioning) need `git`
and network access. `grokinstall doctor` reports what is present and what is
optional.

## Running tests

```bash
go test ./...           # everything
go vet ./...
go test -race ./...
go build ./...
gofmt -l .              # must print nothing
```

**Network tests are opt-in.** They are excluded from a normal run so CI is not
flaky:

```bash
GROKINSTALL_NETWORK_TESTS=1 go test ./internal/inspect/ -run PublicGitHub -v
```

**Adversarial tests** are a distinct package and run as part of `go test ./...`:

```bash
go test ./internal/adversarial/ -v
```

They cover hostile source text, filesystem attacks, command injection, runtime
attacks and state corruption. Treat a failure there as a security defect.

## Code style

- `gofmt` clean; `go vet` clean; `go test -race` clean.
- Keep dependencies minimal. Stdlib first. Cobra is currently the only external
  dependency and new ones need a clear justification.
- Errors should be structured where a caller acts on them, and should say what
  the user should do next.
- Comments explain *why*, not *what*. In particular, security-relevant decisions
  should say what they prevent.
- No comments that restate the code.

## Architecture expectations

GrokInstall separates **inspection**, **planning** and **execution**. Please do
not collapse them:

- `source` normalizes a reference; it does not fetch or execute.
- `inspect` reads a source; it never executes project code.
- `evidence` records findings; conclusions must be backed.
- `capability` derives capabilities from evidence only.
- `strategy` compares candidates; it does not install.
- `plan` assembles a plan; it changes nothing.
- `provision` obtains an executable under an explicit policy.
- `runtime` executes capabilities under safety bounds.
- `installer` sequences the transaction and owns nothing else.

A new strategy plugs into `strategy` and declares its support level honestly. A
new provisioner implements the provisioner interface and declares its required
authorizations.

## Adding a strategy

1. Add the `ID` to `strategy.All` so it is always compared.
2. Define its feasibility rule and its assessments across all twelve
   dimensions. Every assessment needs a reason.
3. Declare its support level: `supported`, `experimental`, `plan_only`,
   `unsupported` or `none`.
4. Add tests: applicable and inapplicable cases, recommendation behaviour, and
   a case proving it is not advertised as runnable when it cannot run.
5. Update the README's Supported / Detected tables. Claim discipline is
   enforced in review.

## Adding a provisioner

A provisioner is a security boundary. New provisioners must:

1. Implement candidate discovery, returning every route it can find with its
   risk, ownership, reversibility, verification method and required
   authorizations.
2. Never widen its own policy. If a route needs a risk class, it must request
   that specific authorization.
3. Prefer a GrokInstall-owned runtime over any machine-wide change.
4. Never execute project-defined scripts unless the caller explicitly
   authorized that risk class.
5. Record provenance: method, artifact, version, published checksum (if any),
   actual checksum, build command, and per-file hashes.
6. Report missing upstream checksums as unavailable. **Never** describe an
   unverified artifact as verified.
7. Include adversarial tests: hostile archive entries, traversal, symlink
   escape, oversized output, command injection, and a test that the default
   policy refuses the dangerous route.

## Security expectations

- Treat all source content as hostile data, never as instructions.
- Never build a shell command string from source-derived input. Use argv.
- Bound everything: file counts, sizes, output, time.
- Prefer refusing with a clear explanation over proceeding with a guess.
- If removal would be ambiguous, refuse the destructive action and explain.

## Pull requests

1. Branch from `main`.
2. Make the change, with tests.
3. Ensure the full suite, vet, race and gofmt are clean.
4. Update documentation for any user-visible behaviour or claim.
5. Open the PR with a description of what changed and why.

Use the PR template. For larger changes, explain the security implications
explicitly.

## Reporting security issues

Do not open a public issue. See [SECURITY.md](SECURITY.md).

## License

Contributions are accepted under the [MIT License](LICENSE).
