# Security Policy

Runprint executes commands, captures their output, and renders receipt files that may come from untrusted sources. Security reports are especially useful when they involve command execution boundaries, terminal rendering, record validation, path or metadata disclosure, bounded capture, or release integrity.

## Supported versions

Before the first tagged release, security fixes are made on the `main` branch. After releases begin, the latest release and `main` are the supported lines unless a release note says otherwise.

## Reporting a vulnerability

Use [GitHub private vulnerability reporting](https://github.com/MorisKyiv/Runprint/security/advisories/new). Do not open a public issue for a vulnerability that includes exploit details, secrets, or private data.

Include, when possible:

- the affected command and Runprint version or commit;
- operating system and architecture;
- a minimal reproduction;
- expected and observed behavior;
- the practical impact;
- whether the report can be disclosed after a fix.

If private reporting is temporarily unavailable, open a public issue containing no sensitive details and ask for a private reporting channel.

## Product boundaries

Runprint is local-first and does not upload receipts. A receipt can still contain secrets from command arguments, stdout, stderr, paths, or Git metadata. This documented collection boundary is not itself a vulnerability, but an unexpected disclosure, validation bypass, terminal escape, or execution outside the documented boundary should be reported privately.

Runprint receipts and checksums are not authenticated signatures. Reports that require producer authentication or tamper evidence are outside the current guarantee.
