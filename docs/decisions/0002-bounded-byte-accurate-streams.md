# Bounded and byte-accurate stream records

- Status: accepted
- Date: 2026-08-17

## Context

Schema v1 stored stdout and stderr as unbounded Go strings. A noisy command could consume all available memory, and `encoding/json` could replace invalid UTF-8 without recording that the bytes had changed. Adding silent truncation to v1 was not acceptable because an old reader would present a partial stream as complete.

The first bounded design reserved 64 KiB for a head and 192 KiB for a tail, with the tail absent when no bytes were omitted. That design could not represent a complete stream between 64 KiB and 256 KiB. It also placed an omission marker beside untrusted output in a way the output itself could imitate.

## Decision

Schema v2 represents each stream with explicit byte accounting:

```json
{
  "received_bytes": 307200,
  "capture_complete": true,
  "head_bytes": 65536,
  "head_text": "readable prefix",
  "omitted_bytes": 45056,
  "tail_bytes": 196608,
  "tail_text": "readable suffix",
  "tail_raw_base64": "optional exact bytes"
}
```

`received_bytes` means bytes successfully received by Runprint. It does not claim to count bytes a process attempted to emit but never flushed. `capture_complete` says whether Runprint read the pipe to EOF.

The default collector keeps a stream whole in `head` until the combined 256 KiB budget is exceeded. Only then does it retain a 64 KiB prefix and a 192 KiB suffix. Persistent collector capacity is bounded by that budget plus three bytes of UTF-8 boundary context, independent of total input size.

Every chunk has a readable UTF-8 text field. If the exact bytes are not valid UTF-8, the text uses replacement characters and an optional canonical padded RFC 4648 Base64 field preserves the exact bytes. Base64 is rejected when it contains CR or LF, even though Go's standard decoder normally ignores those characters.

Reader invariants are fail-closed:

- the complete record was initially limited to 4 MiB before JSON decoding;
- retained stream bytes are limited to 256 KiB per stream;
- count tokens are canonical non-negative integers no greater than `2^53 - 1`;
- counts must equal the exact retained and omitted byte lengths;
- duplicate keys, unknown v2 fields, invalid raw JSON UTF-8, unpaired JSON surrogates, and nesting deeper than 128 levels are rejected;
- optional raw bytes must decode canonically, match their declared length and readable text, and be present only for invalid UTF-8;
- when `omitted_bytes` is zero, the complete stream is in `head` and `tail` is empty.

UTF-8 boundary bytes move into the omitted region only after actual truncation and only when a split rune can be proven from at most three bytes of retained context. Standalone invalid bytes remain retained and are represented by readable text plus exact Base64.

Safe terminal output prefixes every untrusted line with `| `. Markdown keeps omission metadata outside dynamically sized code fences. Captured content can resemble an omission marker, but it cannot occupy the marker's trusted structural position. `--raw` intentionally gives up this protection for trusted records.

Schema v1 remains readable. Renderers disclose that it had no byte accounting and that invalid UTF-8 may already have been replaced. New writes use v2 only. Working directories inside the current user's home are stored with a `~` prefix so the common absolute-path username leak is not durable in new records.

## Consequences

Records remain useful as plain Markdown even when output contains invalid bytes. Exact retained bytes are recoverable from JSON without making all output opaque Base64. Memory use is predictable for arbitrarily large streams, while medium streams up to 256 KiB remain complete.

The 64/192 KiB split is a capture policy, not a promise that readers should infer from field presence. Readers rely on explicit counts and enforce only the total retained-byte ceiling.

Live passthrough is deliberately separate. It must not use `io.MultiWriter`, which stops after the first writer error. The future tee contract is collector-first and passthrough-best-effort: capture continues after a downstream pipe closes, passthrough is disabled after its first error, and a boolean records that the live consumer disconnected without persisting a potentially sensitive error string.

The 4 MiB whole-file ceiling above was superseded before release by [ADR 0005](0005-close-pre-release-record-contract.md), which raises it to 8 MiB after accounting for adversarial JSON and Base64 expansion.
