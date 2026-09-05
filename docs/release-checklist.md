# Release checklist

Complete every required item for the target environment and attach evidence to
the release record. An unchecked blocker stops promotion.

## Source and verification

- [ ] Release commit is on `main`, reviewed, and all required GitHub checks pass.
- [ ] `make release-check` passes against a disposable PostgreSQL 18 database.
- [ ] OpenAPI, integration contract, migrations, dashboards, alerts, and operator
      documentation match the release commit.
- [ ] Security review has no unresolved high-severity finding; residual risks have
      an owner and an accepted deployment control.
- [ ] No secret, production credential, private endpoint, or real customer data is
      present in the repository, image layers, logs, or build artifacts.

## Artifact and supply chain

- [ ] Server and migration binaries come from the same reviewed commit and image.
- [ ] Final image runs as `65532:65532`, has an SBOM, passes image scanning, and is
      signed or otherwise provenance-verified by the deployment platform.
- [ ] Registry digest is recorded and the deployment references that digest rather
      than a mutable tag.
- [ ] Go module and GitHub Actions dependency changes have been reviewed.

## Database and configuration

- [ ] Backup and restore have been verified for the target database.
- [ ] Migration was rehearsed against representative schema/data volume, including
      lock duration, compatibility, and forward-recovery behavior.
- [ ] Migration and runtime database roles are separate and least-privileged.
- [ ] Production PostgreSQL uses verified TLS; connection and storage capacity are
      sized for the total replica count.
- [ ] `API_TOKEN` and `OUTBOX_DELIVERY_TOKEN` are distinct random secrets from the
      secret manager, and the outbox URL is HTTPS.
- [ ] Worker batches, concurrency, leases, timeouts, and retry delays have been
      reviewed for the target environment.

## Rollout and validation

- [ ] Migration job completed successfully before the new application revision.
- [ ] Canary `/healthz`, `/readyz`, authenticated `/v1/capabilities`, and `/metrics`
      checks pass through the production routing path.
- [ ] An idempotent ticket lifecycle completes and its outbox events are accepted
      and deduplicated by the game backend.
- [ ] Dashboard data is present and alert routing is tested.
- [ ] Canary observation covers HTTP errors, worker failures, queue age, fill speed,
      timeout ratio, and both fairness limits before full rollout.

## Rollback readiness

- [ ] Previous known-good image digest is available.
- [ ] Rollback owner, decision threshold, and communication channel are recorded.
- [ ] Application rollback is compatible with the migrated schema.
- [ ] Operators understand that leases recover abandoned work and outbox delivery
      remains at least once during restart or rollback.

## Release record

Record the release version, commit SHA, image digest, migration catalogue state,
CI run, security review approval, deployment time, operator, canary evidence, and
rollback decision. Production SLOs and performance claims require representative
measurements; synthetic test results must not be substituted.
