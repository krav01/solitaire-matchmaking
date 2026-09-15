# Release candidates

The release workflow accepts two immutable tag forms on reviewed commits contained in `main`:

- stable: `vMAJOR.MINOR.PATCH`, for example `v1.0.0`;
- release candidate: `vMAJOR.MINOR.PATCH-rc.N`, for example `v0.9.0-rc.1`, where `N` starts at 1.

Both forms run the same release validation, backup/restore rehearsal, container build, high/critical vulnerability scan, SBOM generation, checksum verification, GHCR publication, and provenance/SBOM attestation flow. Existing image tags are never overwritten.

A release candidate is pre-production evidence. It does not satisfy the target-environment items in `docs/release-checklist.md`, does not imply production capacity, and must not be promoted to a stable tag without the required environment-specific migration, canary, alert-routing, SLO, rollback, and operational evidence.

Create an RC only from a reviewed green `main` commit:

```bash
git switch main
git pull --ff-only
git tag -a v0.9.0-rc.1 -m "solitaire-matchmaking v0.9.0-rc.1"
git push origin v0.9.0-rc.1
```

The resulting image tag is `ghcr.io/krav01/solitaire-matchmaking:0.9.0-rc.1`. Use the immutable digest recorded by the workflow for any deployment or verification step.
