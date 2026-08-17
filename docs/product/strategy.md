# Product strategy: execution receipts, not bug-report bundles

- Status: accepted
- Date: 2026-08-17
- Review horizon: six weeks after the first public release

## Decision

Runprint will remain a local-first command-line project, but its category is narrower than the original hypothesis.

Runprint is an open execution-receipt contract plus a small reference CLI. A receipt describes one non-interactive command execution with explicit completeness, bounded retained output, strict validation, and a safe human rendering. The useful boundary is not just developer-to-maintainer; it is any handoff between a local machine, CI, a person, and an agent.

The standalone CLI is an adapter and reference implementation, not the whole moat. Long-term value must come from an interoperable receipt contract, trustworthy failure semantics, and integrations that can produce or consume the same artifact.

Runprint will not describe a receipt as proof, verified truth, or a reproducible environment. It is structurally valid and loss-explicit, but the reporter controls the machine and the file. It does not package dependencies or authenticate its author.

## What the market check changed

The problem is real, but generic capture and report generation are already a category:

| Alternative | What it already owns | Implication for Runprint |
| --- | --- | --- |
| [Devtriage](https://github.com/DevMubarak1/Devtriage) | Command output, environment and Git snapshots, ZIPs, Markdown issues, focused tests | `capture + Markdown` is not differentiation. |
| [FailPack](https://failpack.dev/docs) | Local reports, redaction, bundles, history, cloud sync, AI analysis, test generation, TUI | Competing feature-for-feature would turn Runprint into a weaker clone and expand its privacy surface. |
| [git bugreport](https://git-scm.com/docs/git-bugreport) and [envinfo](https://github.com/tabrindle/envinfo) | Domain or environment diagnostics integrated into an existing support flow | Report collection works best when the producer or ecosystem defines what context matters. |
| [Atuin](https://atuin.sh/) and [Warp Blocks](https://docs.warp.dev/terminal/blocks/) | Searchable command history, output context, bookmarks, sharing, terminal-native AI | Runprint should not become a shell-history database or terminal UI. |
| [asciinema](https://docs.asciinema.org/getting-started/) | PTY-backed terminal session recording and replay | Interactive fidelity is a different product with a different trust and portability model. |
| [Develocity Build Scan](https://docs.gradle.com/enterprise/get-started/) | Permanent, shareable, deeply instrumented build records and comparison | Ecosystem-specific instrumentation will always beat a generic wrapper on depth. |
| [ReproZip](https://www.reprozip.org/) | OS-call tracing and packaging binaries, files, and dependencies for reproduction | True reproducibility is much heavier than an execution receipt. |
| [GitHub Actions artifacts](https://docs.github.com/en/actions/concepts/workflows-and-actions/workflow-artifacts) | Hosted logs and arbitrary retained build/test outputs | CI does not need another log store; Runprint is useful only if its portable contract improves handoff outside the CI UI. |
| Coding agents such as [Claude Code](https://docs.anthropic.com/en/docs/claude-code/overview), [Codex CLI](https://developers.openai.com/codex/cli), and [GitHub Copilot agents](https://docs.github.com/en/copilot/how-tos/copilot-on-github/use-copilot-agents/manage-and-track-agents) | Run commands themselves and retain tool/session logs | An agent on the same machine usually does not need Runprint. The opportunity is cross-session and cross-system handoff. |

The direct competitors have limited public adoption evidence, so their existence validates the idea only weakly. It does not validate a market. Runprint must earn repeat use after a public release rather than infer demand from adjacent tools.

## Target users

| Priority | Group | Job to be done | Fit |
| --- | --- | --- | --- |
| 1 | Tool maintainers and integration authors | Accept one predictable artifact from local runs, CI, support, or agents without trusting arbitrary terminal rendering | Strongest fit; they value a strict contract and stable failure semantics. |
| 1 | Developers in repeated failure handoffs | Preserve a failing command once and send the same bounded receipt to a teammate, issue, or remote agent | Good fit when handoff is recurrent and the command can be rerun through a wrapper. |
| 2 | Support, DevOps, and CI owners | Attach a compact vendor-neutral receipt to a ticket or workflow artifact | Useful as an adapter, but existing CI logs already cover basic capture. |
| 2 | Maintainers receiving public reports | Read safe Markdown without installing Runprint; optionally validate the JSON receipt | They receive value but should not bear setup cost. |
| Not primary | AI-assisted or “vibe” developers working with a local agent | Give the agent a failure it could usually execute and observe itself | Weak unless the failure occurred outside the current agent session or environment. |
| Not primary | Interactive terminal users | Record full-screen apps, prompts, colors, and exact interleaving | Requires a PTY/session recorder and conflicts with the current receipt contract. |

## Durable distinction to pursue

The differentiation is the negative-space contract, not the amount of context collected:

1. **Loss is explicit.** Received, retained, omitted, and incomplete bytes cannot be confused.
2. **Resources are bounded on both write and read.** A noisy command or hostile artifact cannot turn capture or inspection into unbounded memory use.
3. **Retained bytes are exact.** Invalid UTF-8 remains recoverable while reports stay readable.
4. **Untrusted rendering is safe by default.** Output cannot forge trusted metadata through terminal controls or report structure.
5. **Interruption is first-class.** A hung command stopped by the user still leaves a best-effort receipt and says that Runprint mediated the signal.
6. **The artifact is addressable.** A validated file has an exact-byte SHA-256 content ID for correlation and change detection.
7. **Collection stays narrow.** No source snapshot, environment variables, secret-scanning promise, account, upload, or model is required.
8. **The contract is public.** Other tools should be able to produce and consume receipts without importing the Runprint CLI.

These properties are copyable. The defensible advantage, if one develops, will be compatibility, integrations, and a reputation for precise semantics—not a proprietary algorithm.

## Product boundaries

Rejected for the core roadmap:

- cloud storage, team workspaces, and hosted share links;
- AI diagnosis or prompt generation;
- heuristic secret scanning that could imply a false safety guarantee;
- broad environment, dependency, or source-code collection by default;
- a TUI or terminal replacement;
- PTY capture in the core record path;
- claims of reproducibility, provenance, or authenticity that the artifact cannot support.

History and comparison remain plausible, but only after the receipt contract is used in real handoffs. A local run ledger could give the reporter direct value and reduce the current two-sided adoption problem; it also creates retention and privacy obligations, so it is not an automatic next feature.

## Roadmap

### Phase 0 — close the receipt contract

- publish the schema v2 specification;
- allow callers to choose an output path;
- add strict `check` with an exact-byte content ID;
- expose the same ID in human reports;
- keep the release set reproducible and cross-platform.

### Phase 1 — test the handoff wedge

- make the repository public and publish `v0.1.0`;
- provide one minimal GitHub Actions adapter and one agent/CLI consumption example;
- dogfood receipts in the project's own failures and issues;
- document real cases where a receipt removed a clarification round trip.

### Phase 2 — only after evidence

- add opt-in local history with explicit retention and stable receipt references;
- compare a failing receipt with a prior successful receipt;
- design schema v3 only when a validated consumer needs OS/architecture or other new durable fields;
- consider language/tool adapters that add domain context outside the core receipt.

## Validation and stop conditions

Runprint will not add telemetry to answer product questions. Public examples, issue links, integration usage, and direct qualitative feedback are sufficient for the first test.

Within six weeks of `v0.1.0`, continue investing only if there is evidence of all three:

1. at least five unrelated external users create more than one receipt;
2. at least three real handoffs use a receipt in an issue, PR, support exchange, CI investigation, or agent transfer;
3. at least two consumers use the JSON contract or `check`, rather than only copying Markdown.

If repeat use exists but machine consumption does not, reposition Runprint as a small report CLI and stop protocol expansion. If repeat use does not exist, do not hide the result behind more features: archive, repurpose the strict-capture library, or choose a different product.

## Current product risk

The largest risk is still reporter-side incentive. The receiver benefits from better context, while the reporter must install a wrapper and often rerun the failure. Content IDs and a strict format do not solve that alone. Integrations, history, or domain-specific adoption must eventually make capture valuable to the person running the command before a maintainer asks for it.
