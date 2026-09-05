# Security policy

## Supported versions

Until the first tagged release, security fixes are applied to the current
`main` branch. Earlier commits and forks are not maintained release lines.

## Reporting a vulnerability

Do not open a public issue for a suspected vulnerability or include secrets,
credentials, production endpoints, or real player data in a report.

Use GitHub's private vulnerability reporting flow under **Security → Advisories
→ Report a vulnerability** when it is available. If that option is unavailable,
contact the maintainer through the repository owner's GitHub profile with only
enough information to establish a private disclosure channel.

Include, when possible:

- the affected commit, endpoint, worker, or data transition;
- prerequisites and minimal reproduction steps;
- the expected and observed behavior;
- potential confidentiality, integrity, availability, or fairness impact;
- whether credentials or production data may have been exposed.

The maintainer will validate the report, coordinate remediation, and publish
appropriate disclosure after a fix is available. Response times depend on
maintainer availability; this project does not promise a formal security SLA.

The service trust boundaries, repository controls, accepted residual risks, and
deployment requirements are documented in the [security model](docs/security.md),
[release security review](docs/security-review.md), and
[deployment guide](docs/deployment.md).
