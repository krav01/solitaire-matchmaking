# Project map

Keep this file compact. It exists to avoid repeatedly rediscovering the repository.

## Purpose

- Product/service purpose: `<one sentence>`
- Main runtime entrypoint: `<path>`
- Primary data store: `<none/postgresql/etc>`
- External integrations: `<list>`

## Areas

| Area | Location | Responsibility | Status |
| --- | --- | --- | --- |
| Process entry | `<path>` | `<responsibility>` | `<status>` |
| Application | `<path>` | `<responsibility>` | `<status>` |
| Domain | `<path>` | `<responsibility>` | `<status>` |
| Transport | `<path>` | `<responsibility>` | `<status>` |
| Persistence | `<path>` | `<responsibility>` | `<status>` |
| Observability | `<path>` | `<responsibility>` | `<status>` |

## Dependency direction

- `<layer A>` may depend on `<layer B>`.
- `<domain/core>` must not depend on adapters/infrastructure.
- Interfaces live at the consumer where practical.

## Critical invariants

- `<invariant 1>`
- `<invariant 2>`
- `<invariant 3>`

## Risk map

| Risk | Guardrail |
| --- | --- |
| `<risk>` | `<test/check/design rule>` |
| `<risk>` | `<test/check/design rule>` |

## Current focus

- Current stage: `<stage>`
- Active high-risk area: `<area or none>`
- Next milestone: `<milestone>`
