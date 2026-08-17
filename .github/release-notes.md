Runprint is an early alpha of the local-first execution-receipt CLI and format.

## Compatibility

- Receipt schema: `v2`.
- Reader compatibility: schema `v1` and `v2`.
- Source builds require Go 1.23 or newer.

## Known limitations

- Commands receive pipes rather than a TTY, and stdin is not forwarded.
- Stdout and stderr are retained separately, so their exact interleaving is not preserved.
- Receipts are structurally validated and content-addressed, but they are not authenticated or proof that a command ran.
- Release binaries are not code-signed or notarized in this alpha.

See the bundled README for installation, privacy, capture limits, and platform-specific guidance.
