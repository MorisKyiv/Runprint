# Decision 0008: Make the execution receipt the product boundary

## Status

Accepted

## Context

The initial product language centered on turning command failures into bug reports. A current market review found direct overlap with Devtriage and FailPack, while terminal products already organize and share command output, CI systems retain logs and artifacts, and coding agents can execute commands in their own sessions.

A broader context bundle would require environment collection, redaction heuristics, source selection, integrations, cloud features, or AI analysis. That increases privacy risk and duplicates products with a head start. A generic capture-to-Markdown workflow is useful but not a durable category by itself.

The implementation already has a narrower and more unusual set of guarantees: bounded and byte-accounted streams, strict readers, safe untrusted rendering, interruption records, capture-first passthrough, and atomic writes. Those guarantees describe a receipt protocol better than a bug-report generator.

## Decision

Runprint is an open execution-receipt contract and a reference recorder/validator for one non-interactive command.

The schema remains intentionally small. Runprint does not collect environment variables or source, scan for secrets, upload records, diagnose failures, or claim to reproduce the command's environment.

Every validated artifact has a content ID in the form `sha256:<64 lowercase hexadecimal digits>`. The digest covers the exact JSON file bytes, including whitespace and the writer's final newline. `runprint check [record.json]` applies the strict bounded decoder and prints this ID only after validation. `show` includes the same ID in terminal and Markdown output.

The ID is not stored inside schema v2, so there is no self-hash cycle and no schema change. It is not a signature, provenance claim, or proof that the recorded command actually ran. It identifies exact bytes; modifying or reformatting a record produces another ID.

`record --output <path> -- <command>` lets integrations retain a named receipt instead of replacing `.runprint/latest.json`. After capturing pre-execution Git state but before starting the child, Runprint write-tests the nearest existing destination ancestor and removes the probe. This catches common invalid or unwritable destinations without leaving a directory that could change the command's own filesystem view. After capture, Runprint creates a missing parent directory with owner-only permissions but does not change permissions on an existing caller-owned directory. Record files remain mode `0600` where the platform supports Unix modes.

The normative schema contract is documented separately from the Go implementation in [Record format v2](../record-format-v2.md).

## Consequences

- A recipient or automated consumer can validate a bounded artifact and correlate exact copies without rendering it.
- CI and other integrations can choose artifact paths without moving Runprint's default file afterward.
- Markdown remains readable without installation, while its content ID points back to the exact JSON source when that source is also shared.
- Whitespace-only edits change an ID. This is deliberate because the ID addresses an artifact, not an abstract semantic record.
- Stable IDs now exist for future history and comparison work without committing schema v2 to a storage design.
- The product must earn adoption through integrations and interoperability. Adding more captured context is not the default growth path.
