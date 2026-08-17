# Runprint

Runprint turns one non-interactive command run into a small, bounded execution receipt that can cross the boundary between a local machine, CI, a maintainer, and an AI agent.

Instead of reconstructing a failure from a command, a screenshot, and prose, Runprint records the command, exit code, timing, working directory, Git state, and captured stdout/stderr into a strict portable JSON artifact.

Runprint is deliberately not a general bug-report bundle, terminal history, cloud service, or AI debugger. Its job is narrower: produce and validate one loss-explicit receipt without uploading anything or collecting source and environment variables.

## Status

Early development. Runprint is a dependency-free Go CLI that can capture and inspect local command runs.

## Workflow

```sh
runprint record -- go test ./...
# recorded .runprint/latest.json (exit 1)
# content id sha256:...

# keep a named receipt for CI or a handoff
runprint record --output artifacts/go-test.json -- go test ./...

# validate an untrusted receipt and print its exact-byte content ID
runprint check artifacts/go-test.json

runprint show
# inspect a specific artifact
runprint show .runprint/latest.json

# create a report the maintainer can read without Runprint
runprint show --format=markdown > runprint-report.md
```

Runprint currently targets non-interactive build, test, lint, and reproduction commands. It does not forward stdin or allocate a terminal. The child writes to pipes, so color, progress rendering, buffering, and other TTY-dependent behavior may differ from a direct terminal run. By default each invocation replaces `.runprint/latest.json`; `--output` writes a named receipt instead.

Records and Markdown reports are structurally validated, not authenticated or tamper-evident. A recipient can inspect their internal consistency but cannot prove who created them or whether they were edited.

`record` captures Git context immediately before the command starts, mirrors the command's stdout and stderr live, writes `.runprint/latest.json`, then exits with the recorded command's exit code. Runprint's own saved-record confirmation goes to stderr, so stdout remains command output. This makes it safe to place in scripts and CI without hiding failures. Each output stream is retained in at most 256 KiB: complete output up to that limit, otherwise a 64 KiB prefix and 192 KiB suffix with explicit byte accounting.

If the command cannot start, Runprint writes no receipt. It returns the conventional shell status `127` when no executable is found and `126` when the executable cannot be invoked, with an explicit diagnostic on stderr. The same portable convention is used on Windows; those numbers are Runprint's interface rather than native Windows process statuses.

Capture has priority over live output. If a downstream stdout or stderr consumer closes early, Runprint disables passthrough for that stream, continues collecting the command, saves the record, and emits a generic warning without exposing the underlying writer error.

Interrupting a recorded command still produces a best-effort artifact. On Unix, Runprint forwards `SIGHUP`, `SIGINT`, `SIGQUIT`, or `SIGTERM` to the command's process group, records the received signal explicitly, drains the captured streams, writes the record atomically, and exits with the conventional `128 + signal` code. A second signal or a two-second grace deadline forces the process group to stop. After interruption, Runprint waits at most 500 ms for stream drains; an escaped descendant or blocked downstream consumer makes that stream incomplete and live passthrough is abandoned so the artifact can still be saved. On Windows, `Ctrl+C` is recorded as an interruption with exit code 130, but only the direct child can be force-terminated.

`show` validates the record format and prints command metadata, Git context, stdout, and stderr in a readable report. With no path, it opens `.runprint/latest.json`. Terminal control characters are escaped by default so an untrusted record cannot rewrite the displayed report. `--raw` writes captured streams without escaping and should only be used with records you trust.

`show --format=markdown` produces a self-contained report for an issue, pull request, or chat. The recipient does not need Runprint to read it.

`check` runs the same bounded, strict decoder as `show` and prints `sha256:<hex>` for the exact validated file bytes. The content ID is useful for correlation and change detection. It is not a signature: anyone who can alter a receipt can compute a new ID.

The complete schema v2 contract is documented in [Record format v2](docs/record-format-v2.md). The product scope and current market decision are recorded in [Product strategy](docs/product/strategy.md).

## Build

Runprint requires Go 1.23 or newer.

```sh
go build -o runprint ./cmd/runprint
```

## Install

Tagged releases provide archives for Linux, macOS, and Windows on amd64 and arm64. Each archive contains the binary, this README, and the Apache 2.0 license. Download the matching archive and `checksums.txt` from [GitHub Releases](https://github.com/MorisKyiv/Runprint/releases), then verify its SHA-256 digest before extracting it. Checksums detect corruption or a mismatched download; because the manifest is published with the archives and is not signed, it is not proof of provenance.

On Linux, verify and extract the selected archive from a terminal:

```sh
archive=runprint_0.1.0_linux_amd64.tar.gz
grep -F "  $archive" checksums.txt > selected-checksum.txt &&
  sha256sum -c selected-checksum.txt
tar -xzf "$archive"
./runprint_0.1.0_linux_amd64/runprint version
```

On macOS, use `shasum` and extract from Terminal rather than Finder's Archive Utility:

```sh
archive=runprint_0.1.0_darwin_arm64.tar.gz
grep -F "  $archive" checksums.txt > selected-checksum.txt &&
  shasum -a 256 -c selected-checksum.txt
tar -xzf "$archive"
./runprint_0.1.0_darwin_arm64/runprint version
```

Alpha binaries are not signed or notarized. If Gatekeeper still blocks a checksum-verified binary that you downloaded from the official release, remove quarantine from that extracted directory and retry:

```sh
xattr -dr com.apple.quarantine runprint_0.1.0_darwin_arm64
```

On Windows, compare `Get-FileHash <archive>.zip -Algorithm SHA256` with the archive's line in `checksums.txt`, then use `Expand-Archive`. Browser downloads can carry Mark of the Web and trigger SmartScreen or Microsoft Defender. Only after verifying the official archive, `Unblock-File <extracted-path>\runprint.exe` removes that marker. Runprint does not bypass or disable platform security controls automatically.

Runprint can also be installed from source after the first public tag:

```sh
go install github.com/MorisKyiv/runprint/cmd/runprint@latest
```

The release workflow builds all six targets in one job, validates each archive, rebuilds them from a different source path with isolated home, temporary, and Go cache directories, and publishes only after the complete uploaded set is downloaded again and passes the local checksum manifest.

A record is designed to be:

- local-first
- bounded and explicit about loss
- byte-accurate for retained output
- safe to render as untrusted input
- machine-validatable and human-readable
- portable across issue, chat, support, and CI boundaries

## Privacy model

Runprint does not upload anything and does not capture environment variables. Records can still contain sensitive data from command arguments, stdout, stderr, the working directory, and Git metadata. Review every report before sharing it; Runprint does not scan for secrets.

Schema v2 records abbreviate the current user's home-directory prefix only in the top-level working-directory field before writing JSON. Paths outside the home directory remain unchanged. Command arguments, stdout, stderr, Git metadata, and exact bytes stored as Base64 are not redacted and can still reveal usernames or secrets. This is a narrow safeguard, not anonymization.

Schema v2 also preserves invalid UTF-8 exactly. Reports always show readable replacement text with a warning; optional Base64 is stored only for a retained chunk whose original bytes are not valid UTF-8. Schema v1 records remain readable with an explicit warning that they have no byte accounting and may already contain replacement characters.

## Current limitations

- stdin is not forwarded and commands do not run in a terminal (TTY);
- pipe output can differ from a command's direct terminal output;
- shell job-control and suspension signals are not mediated;
- stdout and stderr are stored separately, so their exact interleaving is not preserved;
- the default output replaces `.runprint/latest.json`; use `--output` to retain a named receipt;
- a receipt records an execution but does not package its dependencies or prove that another machine can reproduce it.

## License

Runprint is licensed under the [Apache License 2.0](LICENSE).
