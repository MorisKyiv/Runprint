# Decision 0005: Close the pre-release record contract

## Status

Accepted

## Context

Schema v2 rejects unknown fields. That makes records deterministic and
fail-closed, but it also means an additive field cannot be introduced after a
reader has shipped without a new schema version.

Before the first release, v2 did not identify the Runprint build that produced
a record. It also represented a Runprint-forwarded interruption only through a
conventional exit code such as 130 or 143. A command can independently return
the same code, so the artifact could not distinguish those facts.

The readable-text plus exact-Base64 stream representation also has a larger
JSON worst case than its retained byte budget suggests. A chunk containing one
invalid byte stores Base64 for the complete chunk, while otherwise valid
control bytes can each expand to a six-byte JSON escape. The former 4 MiB file
limit left too little room for two adversarial streams plus command metadata.

## Decision

New schema v2 records include `runprint_version`. Release builds inject it at
link time; local builds use `dev`. The strict reader recognizes the field, and
the writer requires it. The reader continues to accept pre-release v2 records
where it is absent and renders their producer as unknown.

When Runprint receives and acts on a termination signal, the record includes an
optional `interruption` object with a canonical signal name. Its presence
distinguishes a Runprint-mediated interruption from a command that chose the
same numeric exit code. It does not claim that every descendant received or
honored the signal.

The complete record ceiling is 8 MiB. Encoded command metadata is capped at
2 MiB, and other metadata strings have smaller explicit bounds. Capture checks
these constraints before starting the child, so an unsupported invocation is
rejected rather than executed without a writable artifact. Invalid UTF-8 in
metadata is rejected instead of being silently replaced by `encoding/json`.

Before an atomic rename, the writer feeds its serialized bytes through the same
strict decoder used by `show`. Therefore every file committed by `WriteFile` is
within the reader ceiling and accepted by the current reader.

Runprint describes records as structured and self-consistent, not verifiable or
tamper-evident. Neither JSON nor Markdown authenticates its author or contents.

## Consequences

- A report identifies both its schema and its producing Runprint version.
- Interrupted runs are no longer inferred from an ambiguous exit code.
- Adversarial retained bytes and realistically large command metadata fit
  within the reader limit, while untrusted input remains bounded.
- Extremely large or non-UTF-8 command metadata is rejected before execution.
- Future additions such as stable IDs and OS/architecture metadata require a
  deliberate schema v3 rather than silently changing v2.
- Pre-release v2 artifacts remain readable, but cannot be rewritten as new v2
  files until a producer version is supplied.
