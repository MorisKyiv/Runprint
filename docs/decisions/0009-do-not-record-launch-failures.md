# Decision 0009: Do not create execution receipts for launch failures

## Status

Accepted

## Context

Schema v2 requires an `exit_code`, but an operating system can reject a command before a child process exists. Treating that wrapper failure as exit code `1` makes it indistinguishable at the CLI boundary from a command that actually started and returned `1`. Writing a normal receipt would be worse: it would claim an execution duration and exit status for a process that never ran.

Making `exit_code` optional would weaken every consumer and reserve ambiguous semantics for a hypothetical feature. Capturing useful launch diagnostics would also require platform context that schema v2 deliberately does not contain.

## Decision

An execution receipt represents a process that started. Runprint does not write a receipt when `Start` fails, and `exit_code` remains required in schema v2.

The CLI uses portable shell conventions for launch failures:

- `127` when no executable is found;
- `126` when the requested executable cannot be invoked;
- an escaped stderr diagnostic names the requested command and preserves the operating-system cause.

Runprint uses the same interface on Windows even though these are not native Windows process statuses. Failures before the start attempt, such as invalid record metadata or an unwritable output destination, remain Runprint errors with status `1`.

Schema v3 will not make `exit_code` optional merely to represent launch failures. If evidence later justifies a broader invocation-receipt category, it requires an explicit outcome model rather than overloading an execution record.

## Consequences

- A present receipt always describes a command that reached the operating system's started-process state.
- Scripts can distinguish missing commands from ordinary command failures without parsing stderr.
- Git context and output-destination preflight can occur before the failed start, but no receipt directory or JSON file remains afterward.
- OS and architecture fields can still be considered in a future schema based on consumer evidence; they are not pulled forward solely for launch errors.
