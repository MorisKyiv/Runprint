# Decision 0007: Publish complete reproducible release sets

## Status

Accepted

## Context

The first public release needs binaries for Linux, macOS, and Windows on both amd64 and arm64. A job matrix can finish and publish targets independently, exposing a partial release when one runner fails. General-purpose release tooling would also add a large dependency surface before Runprint has a stable release contract.

Release artifacts must identify their producer version, include the project's license, and be verifiable without trusting filenames. Workflow dependencies and the compiler version must not move silently between reruns of the same tag.

## Decision

Runprint uses a repository-owned release packager and one Ubuntu release job.

The packager cross-compiles all six targets with `CGO_ENABLED=0`, `-trimpath`, disabled VCS stamping, and an injected tag version. It creates deterministic tar.gz or zip archives containing the binary, README, and license. Archive ordering, timestamps, ownership, modes, gzip headers, and zip metadata are normalized. A sorted `checksums.txt` records SHA-256 for every archive.

CI runs the packager twice with the pinned release compiler and requires identical manifests. The second build uses a source copy at another absolute path plus isolated `HOME`, `TMPDIR`, `GOCACHE`, `GOMODCACHE`, and `GOPATH` directories. The tag workflow repeats that check before publication. Go 1.26.6 is pinned for the release binary because it is the current security-supported patch release; the source remains compatible with the documented Go 1.23 minimum.

The workflow runs only for annotated, canonical release tags whose commit is on the default branch. Build metadata is rejected because GitHub can normalize special characters in uploaded asset names, and release versions are bounded so archive paths fit the selected USTAR format. It creates a draft GitHub release, uploads every local output, and requires every asset to reach the `uploaded` state. The workflow then downloads the complete remote set, confirms that the remote manifest is byte-identical to the local manifest, and verifies every downloaded archive against it before publication.

Release notes state the receipt schema, compatibility and known limitations, pinned Go toolchain, exact tag commit, and checksum manifest. The checksums detect corruption and mismatched assets; because they are unsigned and distributed in the same release, they are not an authenticity or provenance mechanism.

Cancellation signals are converted to failing shell exits so the ordinary cleanup trap deletes a created draft. A force-kill can still prevent all process cleanup. Recovery is to delete any remaining draft with `gh release delete TAG --yes` and rerun the original workflow. If rerun is unavailable, an owner may delete the remote tag and push the same annotated local tag again; this is an explicit recovery action, not part of the workflow.

Official GitHub actions are pinned to immutable commit SHAs. The workflow uses the runner's GitHub CLI instead of a third-party publishing action.

## Consequences

- No platform can become public before all other supported platforms are ready.
- Rebuilding the same source, version, and pinned toolchain across the isolated paths and environment covered by CI produces the same archive checksums.
- Users can detect corrupted or mismatched downloads with a small checksum manifest without treating it as a signature.
- Release creation is intentionally serialized and takes longer than a matrix build.
- A new Go security patch requires an explicit reviewed workflow change, making the resulting binary provenance visible in Git history.
- The first tag remains a deliberate owner action after the repository is made public; this decision prepares the mechanism but does not create that tag.
