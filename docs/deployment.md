# Deployment guide

This guide describes the first production deployment of the matchmaking service.
`compose.yaml` is a loopback-only development environment and is not a production
topology.

## Runtime topology

Deploy the immutable service image behind a private load balancer or ingress that
terminates TLS. The service process exposes HTTP on `HTTP_ADDR`, runs the API and
all four background workers, and connects directly to PostgreSQL. The game backend
is the only intended business-API caller and the only outbound-event receiver.

Multiple service replicas are supported by transactional claims, lease fencing,
idempotency constraints, and aggregate-ordered outbox delivery. Size
`DB_MAX_CONNS` so the sum across all replicas and migration jobs remains below the
database connection budget.

## Prerequisites

- PostgreSQL 18 with backups, point-in-time recovery, and TLS enabled;
- a private network path between ingress, service, database, and game backend;
- an external secret manager for `DATABASE_URL`, `API_TOKEN`, and
  `OUTBOX_DELIVERY_TOKEN`;
- an HTTPS outbox endpoint whose receiver persists `Idempotency-Key` values;
- Prometheus access to authenticated `/metrics` and the supplied alert rules;
- a container registry that supports immutable digests and vulnerability scanning.

Run the service with a read-only root filesystem, all Linux capabilities dropped,
`no-new-privileges`, the platform default seccomp profile, no host mounts, and the
numeric image user. Grant only network access to the private ingress, PostgreSQL,
the configured outbox endpoint, DNS, and required telemetry infrastructure.

Use separate random values of at least 32 characters for the two bearer tokens.
The process rejects token reuse. Use `sslmode=verify-full` with the required CA and
hostname configuration in the production `DATABASE_URL`; `sslmode=disable` in the
example files is for loopback development only.

## Build and promotion

The repository publishes release images through
`.github/workflows/release.yml`. A push of a new stable `vMAJOR.MINOR.PATCH` tag
starts the workflow; publication proceeds only when the tag points to a commit
contained in `main`. The workflow:

1. runs `make release-check` with PostgreSQL 18;
2. rehearses a checksummed backup, restore and zero-pending migration run against
   100,000 representative rows per primary operational table;
3. builds and checks the non-root image in a read-only-permission job;
4. blocks publication on high or critical image vulnerabilities;
5. generates an SPDX JSON SBOM and uploads checksummed release inputs;
6. verifies those checksums in a separate privileged job and refuses to
   overwrite an existing GHCR version tag;
7. pushes the image, records its registry digest, and publishes signed build
   provenance and SBOM attestations.

Create a release tag only from a reviewed, green `main` commit:

```bash
git switch main
git pull --ff-only
git tag -a v0.1.0 -m "solitaire-matchmaking v0.1.0"
git push origin v0.1.0
```

The workflow publishes
`ghcr.io/krav01/solitaire-matchmaking:<version>` but does not deploy it, create a
database job, or change an environment. Copy the immutable
`ghcr.io/krav01/solitaire-matchmaking@sha256:...` reference from the workflow
summary into the release record and deployment configuration. The runtime image
contains only the statically linked `server` and `migrate` binaries plus CA
certificates, and runs as numeric user and group `65532:65532`.

After authenticating to GHCR when required, verify the signed provenance before
promotion:

```bash
IMAGE=ghcr.io/krav01/solitaire-matchmaking@sha256:replace-with-recorded-digest
gh attestation verify "oci://${IMAGE}" -R krav01/solitaire-matchmaking
docker pull "${IMAGE}"
```

## Database migration

Application startup never changes the schema. Run one migration job with the same
image before starting the new application revision:

```bash
IMAGE=ghcr.io/krav01/solitaire-matchmaking@sha256:replace-with-promoted-digest
docker run --rm \
  --read-only \
  --user 65532:65532 \
  --entrypoint /migrate \
  --env DATABASE_URL \
  "$IMAGE"
```

Use a dedicated migration role with schema-change privileges. Give the runtime
role only the table and sequence privileges required by the service. Migrations
are checksummed and serialized by an advisory lock, but the rollout must still
follow `docs/migration-safety.md` and have a verified backup/restore path.
Attach the automated rehearsal report to the release record, then repeat the
restore against the target environment's backup mechanism and representative
data before promotion; synthetic CI evidence does not establish target RTO or
PITR readiness.

## Rollout sequence

1. Complete `docs/release-checklist.md` and capture the evidence links.
2. Back up PostgreSQL and verify the restore procedure for the target environment.
3. Run the migration job and stop if it returns non-zero.
4. Deploy one canary replica with the immutable image digest.
5. Wait for `GET /readyz` to return 200, then verify authenticated capabilities
   and metrics.
6. Exercise one idempotent ticket lifecycle and confirm outbox receipt and
   deduplication at the game backend.
7. Watch error, worker-failure, queue-age, fill-speed, and fairness alerts before
   increasing traffic or replicas.
8. Complete the rollout only while the canary remains healthy.

Use `/healthz` for liveness and `/readyz` for readiness. Give shutdown at least
`SHUTDOWN_TIMEOUT` plus platform scheduling margin. On termination the service
marks readiness false, drains HTTP requests, cancels workers, waits for them, and
then closes the database pool.

## Rollback

Rollback the application to the previously promoted image digest when canary
errors, worker failures, queue growth, or fairness alerts breach the agreed
release threshold. Do not reverse a migration by editing the migration catalogue
or deleting its history row. Forward-fix additive schema changes; destructive
cleanup belongs to a later release after compatibility is proven.

An interrupted worker may leave leased work. Replacement replicas recover it
after the lease expires; stale acknowledgements are fenced. Outbox delivery is at
least once, so the receiver must remain idempotent during rollback.
