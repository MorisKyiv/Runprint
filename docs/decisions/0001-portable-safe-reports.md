# Portable and terminal-safe reports

- Status: accepted
- Date: 2026-08-17

## Context

Runprint initially stored a useful JSON record but required the recipient to install Runprint to read it comfortably. That placed the setup cost on both the person reporting a failure and the maintainer receiving it.

The original `show` renderer also wrote captured strings directly to the terminal. A record is untrusted input: stdout, stderr, command arguments, paths, and Git branch names can contain control sequences that move the cursor, erase prior text, or make fabricated output appear to belong to the report.

## Decision

1. `runprint show` escapes non-printing Unicode and terminal control characters by default while preserving ordinary printable Unicode, newlines, and tabs.
2. `runprint show --raw` is an explicit escape hatch for trusted records and is only available with terminal-format output. Metadata remains escaped.
3. `runprint show --format=markdown` emits a self-contained report that can be pasted into an issue without requiring the recipient to install Runprint.
4. Markdown code fences grow to exceed any backtick run in captured content, preventing content from closing its own fence.
5. Markdown output abbreviates common home-directory prefixes, but carries an explicit warning that Runprint does not scan for secrets.
6. `.runprint` is created with owner-only permissions (`0700`); record files remain `0600`.

## Consequences

The default terminal view prioritizes trustworthy presentation over byte-for-byte visual fidelity. Visible escapes such as `\x1b` are evidence that the source contained a control character. Users who need the original terminal behavior must opt in with `--raw` and accept the risk.

Markdown removes the recipient-side installation barrier but does not make a record safe to publish. Command arguments and captured output can still contain credentials, tokens, proprietary source, usernames, host paths, or other sensitive data.

## Review decisions outside this change

The independent review that prompted this decision also identified capture and schema concerns. They are deliberately separated from the renderer change:

- **Completed:** Git context is collected before the command and describes pre-execution state.
- **Completed in [ADR 0002](0002-bounded-byte-accurate-streams.md):** Schema v2 provides bounded, byte-accounted capture without making v1 readers present partial output as complete.
- **Confirmed, separate work:** Interrupted commands need a best-effort artifact and the correct signal-derived exit behavior.
- **Deferred:** A stable record ID belongs with history and retention design, not the capture-limit patch.
- **Deferred:** OS and architecture fields need a concrete reproduction use case and privacy review before entering the durable schema.
- **Rejected for core:** Heuristic secret scanning would create false confidence. Runprint will first make captured fields and sharing risks explicit; user-supplied redaction rules may be considered later.
- **Rejected for the first stable format:** PTY emulation changes command behavior and portability. Non-interactive pipe capture remains the initial contract.

Keeping these changes separate makes the format transition and signal semantics independently reviewable.
