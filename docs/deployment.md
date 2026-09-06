# Deployment guide

This guide describes the first production deployment of the matchmaking service.
`compose.yaml` is a loopback-only development environment and is not a production
topology.

## Runtime topology

Deploy the immutable service image behind a private load balancer or ingress that
terminates TLS. The service process exposes HTTP on `HTTP_ADDR`, runs the API and
all five background workers, and connects directly to PostgreSQL. The game backend
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

1. runs `make release-check` with PostgreSQL 18, including the full external
   canary lifecycle against a separate disposable database;
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
contains only the statically linked `server`, `migrate` and `shadow-report`
binaries plus CA
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

## Rating shadow rollout

Migration `000007` is additive. Existing rooms keep a null `deck_version` and
their shadow work is safely skipped. Before enabling a candidate, the trusted
room/deck provisioning path must persist the immutable deck version on every new
room. Do not infer it from the deck instance id.

Register the candidate in `rating_models`, then insert one
`rating_shadow_deployments` row in a reviewed operation. Its nested `candidate`
definition must contain a complete `rating.ExtendedConfig`: explicit baseline
parameters, feature schema, training-only means and standard deviations,
coefficients, and a training horizon at or before the cutoff. The database
rejects overlapping active deployments for the same mode/rules/deck context.
End a run by setting `ended_at`; never rewrite its definition or training
boundary after predictions exist.

Monitor the `rating_shadow` worker and compare the age of the oldest unfinished
timeline item with the pilot window. Generate evidence from the promoted image:

```bash
docker run --rm \
  --read-only \
  --user 65532:65532 \
  --entrypoint /shadow-report \
  --env DATABASE_URL \
  --env RATING_SHADOW_COMPARISON_POLICY \
  "$IMAGE" -candidate-version rating-extended-v1
```

Archive the JSON report with its deployment definition and observation window.
An eligible report is evidence for a separate reviewed activation decision; the
shadow worker and report command never change the active model.

## Rollout sequence

1. Complete `docs/release-checklist.md` and capture the evidence links.
2. Back up PostgreSQL and verify the restore procedure for the target environment.
3. Run the migration job and stop if it returns non-zero.
4. Deploy one canary replica with the immutable image digest.
5. Wait for `GET /readyz` to return 200, then verify authenticated capabilities
   and metrics.
6. Exercise one idempotent ticket lifecycle and confirm outbox receipt and
   deduplication at the game backend.
7. Load the private-pilot SLO recording rules, confirm their dashboard series,
   and watch error-budget, worker-failure, database-pool, fill-speed and fairness
   alerts before increasing traffic or replicas.
8. Complete the rollout only while the canary remains healthy.

The exact pilot objectives and minimum evidence gates are defined in
`docs/slo.md`. The automated canary artifact is pre-release evidence that the complete software
path works against the example receiver. Repeat steps 5–7 through the target
routing path and production game backend; the synthetic receiver and disposable
database do not prove environment networking, durable receiver transactions,
alert routing or production capacity.

Use `/healthz` for liveness and `/readyz` for readiness. Give shutdown at least
`SHUTDOWN_TIMEOUT` plus platform scheduling margin. On termination the service
marks readiness false, drains HTTP requests, cancels workers, waits for them, and
then closes the database pool.

## Rollback

Rollback the application to the previously promoted image digest when canary
errors, worker failures, ticket-assignment latency, timeout, database-pool or
fairness alerts breach the agreed release threshold. Do not reverse a migration
by editing the migration catalogue or deleting its history row. Forward-fix
additive schema changes; destructive cleanup belongs to a later release after
compatibility is proven.

An interrupted worker may leave leased work. Replacement replicas recover it
after the lease expires; stale acknowledgements are fenced. Outbox delivery is at
least once, so the receiver must remain idempotent during rollback.
