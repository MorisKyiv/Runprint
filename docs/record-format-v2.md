# Runprint record format v2

This document specifies the durable JSON artifact written by Runprint before its first public release. Readers must fail closed on malformed or ambiguous input. Writers must emit records accepted by the same strict reader before replacing a destination file. The reference CLI also write-tests the nearest existing destination ancestor after capturing Git context and before starting the recorded child, then removes the probe so the command does not observe a newly created receipt directory.

## Scope and claims

A record describes one observed non-interactive command execution. The child process must have started; a writer must not emit a normal v2 record for lookup, permission, executable-format, or other launch failures. It can establish internal consistency, byte accounting, and whether retained output was complete. It cannot establish who ran the command, whether the machine or file was honest, or whether another environment can reproduce the result.

## Top-level object

Unknown fields are rejected. Unless marked optional, every field is required and may not be `null`.

| Field | JSON type | Meaning |
| --- | --- | --- |
| `version` | integer | Exactly `2`. |
| `runprint_version` | string | Producer build version. Required for new writes; the reader accepts its absence only for pre-release v2 compatibility. |
| `command` | array of strings | Executable followed by its arguments. The array is non-empty. It is an argv vector, not a shell command string. |
| `directory` | string | Working directory recorded before execution. Writers may replace the current user's home prefix with `~`. |
| `started_at` | string | RFC 3339 timestamp. The reference writer emits UTC with nanosecond precision when present. |
| `duration_ns` | integer | Non-negative elapsed wall-clock nanoseconds. |
| `exit_code` | integer | Required status of the started command, or the conventional signal-derived status when Runprint mediated an interruption. |
| `interruption` | object, optional | A termination signal received and acted on by Runprint. |
| `stdout` | stream object | Bounded stdout receipt. |
| `stderr` | stream object | Bounded stderr receipt. |
| `git` | object, optional | Git state captured before the child starts. |

Command JSON encoding is limited to 2 MiB. Individual metadata strings are valid UTF-8 and bounded; the current implementation limits the directory, Git commit, and Git branch to 64 KiB each and the producer version to 128 bytes.

## Stream object

Each stream is loss-explicit and byte-accounted.

| Field | JSON type | Meaning |
| --- | --- | --- |
| `received_bytes` | count | Bytes successfully read by Runprint. It does not include bytes a process attempted but failed to flush. |
| `capture_complete` | boolean | `true` only when Runprint read the pipe to EOF. |
| `head_bytes` | count | Exact byte length represented by the head fields. |
| `head_text` | string | Readable UTF-8 representation of the retained head. |
| `head_raw_base64` | string, optional | Canonical padded RFC 4648 Base64 of the exact head, present only when the exact bytes are not valid UTF-8. |
| `omitted_bytes` | count | Captured bytes intentionally not retained between head and tail. |
| `tail_bytes` | count | Exact byte length represented by the tail fields. |
| `tail_text` | string | Readable UTF-8 representation of the retained tail. |
| `tail_raw_base64` | string, optional | Canonical padded RFC 4648 Base64 of the exact tail, present only when the exact bytes are not valid UTF-8. |

A count is a canonical non-negative JSON integer from `0` through `2^53 - 1`. A leading sign, leading zero, decimal point, or exponent is invalid.

The following equations and limits hold:

```text
received_bytes = head_bytes + omitted_bytes + tail_bytes
head_bytes + tail_bytes <= 262144
```

When `omitted_bytes` is zero, the complete retained stream is stored in `head` and every tail field is empty or absent as appropriate. Once the 256 KiB retention budget is exceeded, the reference writer normally retains a 64 KiB prefix and a 192 KiB suffix. Readers must rely on explicit counts, not infer policy from those sizes.

If exact chunk bytes are valid UTF-8, the text is those bytes and the raw Base64 field must be absent. Otherwise, the text replaces each invalid sequence with U+FFFD and canonical Base64 preserves the exact retained bytes. Base64 containing CR or LF is rejected.

`capture_complete: false` and `omitted_bytes > 0` express different facts. The former means Runprint did not observe the entire pipe. The latter means Runprint observed bytes but deliberately did not retain all of them.

## Interruption object

The object has one required field:

| Field | JSON type | Meaning |
| --- | --- | --- |
| `signal` | string | Canonical portable signal name handled by Runprint. |

Supported values and required exit codes are:

| Signal | Exit code |
| --- | ---: |
| `SIGHUP` | 129 |
| `SIGINT` or Windows `interrupt` | 130 |
| `SIGQUIT` | 131 |
| `SIGTERM` | 143 |

Presence means Runprint received and acted on the signal. It does not claim that every descendant received or honored it. A command that independently exits with the same numeric code has no `interruption` object.

## Git object

The object is omitted when the working directory is not readable as a Git worktree.

| Field | JSON type | Meaning |
| --- | --- | --- |
| `commit` | string, optional | `HEAD` before command start. |
| `branch` | string, optional | Current branch before command start; absent or empty for detached HEAD. |
| `dirty` | boolean | Whether porcelain status was non-empty before command start. Required whenever `git` is present. |

Git context is intentionally pre-execution. A command that changes the worktree does not retroactively change this object.

## Reader limits and strictness

Before JSON decoding, the complete file is limited to 8 MiB. A reader rejects:

- invalid raw UTF-8 in the JSON document;
- more than one JSON value;
- duplicate keys at any depth, including escaped spellings of the same key;
- unknown v2 fields;
- unpaired JSON surrogate escapes;
- nesting deeper than 128 levels;
- missing required fields or explicit `null` where a value is required;
- byte counts, retained chunks, Base64, text, interruption, or metadata that violate the invariants above.

These rules make resource use and ambiguity bounded. They do not make the contents trustworthy.

## Exact-byte content ID

After a file passes the strict reader, its content ID is:

```text
sha256:<lowercase hexadecimal SHA-256 of every file byte>
```

Whitespace, object-key order, and a final newline are bytes and therefore affect the ID. The reference writer uses deterministic field order, two-space indentation, and one final LF. `runprint check` prints an ID only after validation.

The ID supports correlation, deduplication of exact copies, and change detection when an expected ID is communicated separately. It does not authenticate an author. An editor can change a file and compute its new ID.

## Example

```json
{
  "version": 2,
  "runprint_version": "v0.1.0",
  "command": [
    "go",
    "test",
    "./..."
  ],
  "directory": "~/src/project",
  "started_at": "2026-08-17T12:00:00Z",
  "duration_ns": 1500000000,
  "exit_code": 1,
  "stdout": {
    "received_bytes": 0,
    "capture_complete": true,
    "head_bytes": 0,
    "head_text": "",
    "omitted_bytes": 0,
    "tail_bytes": 0,
    "tail_text": ""
  },
  "stderr": {
    "received_bytes": 12,
    "capture_complete": true,
    "head_bytes": 12,
    "head_text": "test failed\n",
    "omitted_bytes": 0,
    "tail_bytes": 0,
    "tail_text": ""
  },
  "git": {
    "commit": "0123456789abcdef",
    "branch": "main",
    "dirty": true
  }
}
```

## Compatibility

Schema v1 remains readable for migration. It stored stdout and stderr as unbounded strings, had no byte accounting, and could already have replaced invalid UTF-8. New writers must not emit v1. Any new durable field after the closed v2 contract requires a new schema version.
