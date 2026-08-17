# Contributing to Runprint

Runprint is an early-stage, local-first CLI for producing strict, bounded execution receipts. Focused bug reports, portability fixes, tests, and small improvements are welcome.

## Development

Runprint requires Go 1.23 or newer and currently uses only the Go standard library.

Before opening a pull request, run:

```sh
gofmt -w .
go test ./...
go vet ./...
```

Keep changes focused and include tests for behavior changes.

## Design constraints

Changes should preserve these product guarantees:

- no automatic uploads;
- bounded memory and bounded receipt size;
- explicit accounting for omitted or incomplete output;
- strict, fail-closed schema validation;
- safe rendering of untrusted receipt content;
- preservation of the recorded command's exit semantics;
- portable behavior across Linux, macOS, and Windows where practical.

A change to the record contract, compatibility policy, privacy boundary, or release trust model should include an ADR or an update to the existing contract documentation.

## Pull requests

Explain the user-visible behavior, the reason for the change, and the checks you ran. Prefer one coherent change per pull request. Draft pull requests are appropriate for early design feedback.

For security vulnerabilities, follow [SECURITY.md](SECURITY.md) instead of opening a public issue with exploit details.

By contributing, you agree that your contribution is licensed under the repository's Apache License 2.0.
