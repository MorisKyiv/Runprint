# Decision 0004: Make live output subordinate to capture

## Status

Accepted

## Context

Hiding a command's output until it exits makes `runprint record` unsuitable for slow builds, test suites, and other work where progress matters. Runprint needs live stdout and stderr without weakening the guarantee that a record survives downstream output failures.

`io.MultiWriter` does not provide that guarantee. It writes to destinations in order and returns at the first error. If a pipe consumer exits early, a tee built from `io.MultiWriter` can stop the pipe-draining loop and lose the rest of the command output along with the record.

Unix adds another failure mode: writing to a closed stdout or stderr pipe can deliver `SIGPIPE` to Runprint before the write returns `EPIPE`.

## Decision

Each captured stream uses one capture-first sink:

1. Accept the complete chunk into the constant-memory collector.
2. If live passthrough is enabled, offer the same chunk to its output writer.
3. On any passthrough error or short write, disable passthrough for that stream.
4. Always report the full chunk as accepted to the command pipe after capture succeeds.

Passthrough failure never changes stream byte accounting, `capture_complete`, the command exit code, or whether the record is written. The CLI reports only a generic boolean warning; it does not persist an output-writer error string that could contain sensitive destination details. Schema v2 remains unchanged.

In record mode, the CLI subscribes to `SIGPIPE` on Unix so a closed downstream pipe becomes a normal passthrough write failure instead of terminating Runprint.

Child stdout is mirrored only to Runprint stdout. Child stderr, passthrough warnings, and the final saved-record confirmation use Runprint stderr. This keeps successful stdout byte-for-byte free of Runprint diagnostics.

## Consequences

- Long-running commands show progress while retaining bounded capture.
- `runprint record -- command | head` can lose live output after `head` exits but still produces the complete bounded artifact.
- During ordinary execution, a slow writer still applies pipeline backpressure. After interruption or context cancellation, Decision 0006 bounds how long that blocking can delay the artifact.
- Exact stdout/stderr interleaving remains unavailable because the streams are captured and forwarded independently.
