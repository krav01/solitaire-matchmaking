# PostgreSQL migrations

The foundation verifies database connectivity but does not create domain tables.
Versioned SQL migrations and an external migration runner are added with the
transactional tournament stage after the lifecycle rules have been finalized.
Apply migrations explicitly; application startup must not modify the schema.

Required constraints and access patterns are recorded in `docs/data-model.md`.
