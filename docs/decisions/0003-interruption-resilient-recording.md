# Decision 0003: Preserve records across command interruption

## Status

Accepted

The process and drain behavior remains accepted. The original decision to
represent interruption only through an exit code was superseded before the
first release by [ADR 0005](0005-close-pre-release-record-contract.md), which
adds explicit interruption metadata to schema v2.

## Context

The most valuable failure is often the one a developer interrupts because a command hangs. Previously, Runprint used `exec.CommandContext` and only wrote the record after `cmd.Run` returned. The operating system could terminate Runprint on `Ctrl+C` before `.runprint/latest.json` was written, losing the evidence the tool exists to preserve.

Signal handling also has to cover a command's descendants. Signaling only the immediate process can leave a shell child running with inherited stdout or stderr descriptors, preventing capture from reaching EOF indefinitely.

At the time of this decision, schema v2 was considered sufficient for the
increment because:

- `exit_code` can carry the conventional `128 + signal` status;
- `capture_complete` distinguishes EOF from a locally abandoned read;
- `received_bytes` explicitly counts only bytes Runprint actually read.

That was sufficient to stop losing interrupted records, but not to distinguish
a Runprint-mediated interruption from a command that independently returns the
same numeric code. ADR 0005 corrects that ambiguity while v2 is still
pre-release.

## Decision

In CLI record mode, Runprint subscribes to supported termination signals for the full capture-and-write operation.

On Unix:

1. The command starts in an isolated process group.
2. The first `SIGHUP`, `SIGINT`, `SIGQUIT`, or `SIGTERM` received by Runprint is forwarded to that group.
3. The recorded and returned status is `128 +` the first signal, even if a command handler later exits zero. A user-requested cancellation must not appear successful.
4. A second supported signal, or a two-second grace deadline, sends `SIGKILL` to the group.

On Windows, Go cannot send `os.Interrupt` to an arbitrary child. Runprint force-terminates the direct child and records status 130. Descendant termination is not guaranteed.

Runprint owns the stdout and stderr pipes instead of relying on `os/exec`'s internal copy goroutines. Readers continuously feed the existing constant-memory collectors. After the direct process exits, Runprint allows 500 ms for both streams to reach EOF. Any stream still open is closed locally and recorded with `capture_complete: false`; already received bytes remain valid and byte-accounted.

The CLI keeps signal notification active until the validated schema v2 record has been atomically replaced. Further signals during the short write window cannot expose a partially written latest record.

## Consequences

- `Ctrl+C` and termination signals produce a useful artifact instead of reliably losing it.
- Ignored signals and inherited pipe descriptors cannot block Runprint indefinitely.
- Exit code 130/143 preserves shell convention; explicit interruption metadata
  now distinguishes the Runprint-mediated case.
- Process-group behavior is stronger on Unix than Windows.
- TTY input and shell job control remain separate work.
