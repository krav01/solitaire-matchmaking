ALTER TABLE matchmaking_tickets
    ADD COLUMN aggregate_version bigint NOT NULL DEFAULT 1,
    ADD CONSTRAINT matchmaking_tickets_aggregate_version_positive
        CHECK (aggregate_version > 0),
    ADD CONSTRAINT matchmaking_tickets_request_digest_sha256
        CHECK (request_digest ~ '^[0-9a-f]{64}$');

ALTER TABLE room_memberships
    ADD COLUMN assignment_id text NOT NULL,
    ADD COLUMN request_digest text NOT NULL,
    ADD COLUMN ticket_version bigint NOT NULL,
    ADD COLUMN room_version bigint NOT NULL,
    ADD COLUMN room_filled boolean NOT NULL,
    ADD COLUMN result_deadline timestamptz,
    ADD CONSTRAINT room_memberships_assignment_id_present
        CHECK (assignment_id <> ''),
    ADD CONSTRAINT room_memberships_request_digest_sha256
        CHECK (request_digest ~ '^[0-9a-f]{64}$'),
    ADD CONSTRAINT room_memberships_versions_positive
        CHECK (ticket_version > 0 AND room_version > 0),
    ADD CONSTRAINT room_memberships_fill_result_consistent
        CHECK ((room_filled AND result_deadline IS NOT NULL) OR (NOT room_filled AND result_deadline IS NULL)),
    ADD CONSTRAINT room_memberships_assignment_id_unique UNIQUE (assignment_id);

CREATE TABLE ticket_commands (
    ticket_id text NOT NULL REFERENCES matchmaking_tickets(ticket_id),
    command_id text NOT NULL,
    command_type text NOT NULL,
    request_digest text NOT NULL,
    resulting_status text NOT NULL,
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY (ticket_id, command_id),
    CONSTRAINT ticket_commands_identity_present CHECK (command_id <> ''),
    CONSTRAINT ticket_commands_type_supported CHECK (command_type = 'cancel'),
    CONSTRAINT ticket_commands_digest_sha256 CHECK (request_digest ~ '^[0-9a-f]{64}$'),
    CONSTRAINT ticket_commands_result_supported CHECK (resulting_status = 'cancelled')
);
