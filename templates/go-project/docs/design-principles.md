# Design principles: Clean Code, DDD and Clean Architecture

These principles guide structure and reviews. They are not excuses for unnecessary abstractions. Prefer the simplest design that preserves domain clarity, testability, and change safety.

## Clean Code

- Name things by intent and domain meaning, not implementation accident.
- Keep functions focused on one coherent responsibility; extract only when it improves meaning or reuse.
- Prefer explicit control flow over clever compactness.
- Reduce parameter lists by improving boundaries or introducing meaningful value objects, not generic option bags by default.
- Avoid boolean parameters when they hide multiple behaviors; prefer separate operations or domain types.
- Keep comments for rationale, invariants, non-obvious constraints, and externally imposed quirks; do not narrate obvious code.
- Remove dead code and stale comments instead of preserving speculative paths.
- Avoid premature generalization. Duplicate a small amount of code rather than introduce the wrong abstraction.
- Keep errors actionable and contextual without repetitive logging at every layer.
- Make side effects visible in names and boundaries.

## DDD

Use DDD selectively where business rules and terminology justify it.

- Establish a ubiquitous language and reuse domain terms consistently in code, API, docs, tests, and discussions.
- Model important business concepts as explicit types instead of primitive strings/ints when doing so protects invariants.
- Keep entities responsible for identity and invariant-preserving state transitions.
- Use value objects for immutable concepts defined by their value and validation rules.
- Treat aggregate boundaries as consistency boundaries; keep them as small as the invariant allows.
- Modify aggregate state through invariant-preserving behavior rather than arbitrary field mutation.
- Repositories expose domain-oriented persistence operations for aggregate access; they are not generic CRUD frameworks.
- Domain services are for domain behavior that does not naturally belong to one entity/value object, not a dumping ground for orchestration.
- Application/use-case services coordinate domain objects, persistence, transactions, authorization and external effects; they should not become a second domain model.
- Infrastructure concerns stay outside the domain model.
- Domain events represent meaningful completed domain facts; avoid emitting technical CRUD events as domain events without a real business meaning.
- Define bounded contexts when terminology or models diverge materially. Do not force one canonical model across unrelated domains.

## Clean Architecture

Dependency direction points toward business policy.

Typical direction:

`transport / infrastructure -> application -> domain`

- Domain code must not depend on HTTP frameworks, SQL drivers, message brokers, cloud SDKs, or deployment tooling.
- Application code depends on domain concepts and small ports/interfaces needed by use cases.
- Infrastructure adapters implement those ports and translate external representations to internal ones.
- Composition/wiring belongs at the process boundary (`cmd/...` or equivalent).
- Interfaces are introduced at consumption boundaries when they decouple policy from mechanism or improve testing; do not create interfaces for every struct.
- Transport DTOs, persistence rows, and domain models may differ. Convert explicitly where their responsibilities differ.
- Avoid leaking database transaction objects, HTTP request types, or broker-specific messages deep into domain logic.
- Cross-layer shortcuts require an explicit rationale and, for durable exceptions, an ADR.

## SOLID applied pragmatically in Go

- Single responsibility: package/type/function should have a coherent reason to change.
- Open/closed: prefer composition and small extension points where variation is real; do not predict hypothetical variation.
- Liskov: implementations must preserve the behavioral contract expected by consumers.
- Interface segregation: small consumer-owned interfaces are preferred.
- Dependency inversion: policy depends on abstractions at the boundary, while concrete adapters depend inward.

## Package design

- Organize packages around capabilities/domain concepts rather than generic folders such as `utils`, `helpers`, `common`, or `managers`.
- Keep package APIs small; unexport implementation details by default.
- Avoid package cycles and hidden bidirectional dependencies.
- Keep shared packages rare. A shared abstraction needs a stable, genuinely shared concept.
- Prefer duplication across bounded contexts over coupling contexts through an unstable shared model.

## Review questions

For every non-trivial change ask:
- Is the domain rule expressed in the domain vocabulary?
- Is the invariant enforced in the right place?
- Is orchestration separated from domain policy?
- Does dependency direction still point inward?
- Did we add an abstraction because it is needed now or because it might be useful someday?
- Could a new engineer explain why this code exists and where it belongs?
- Is the chosen design easier to test and change without increasing accidental complexity?
