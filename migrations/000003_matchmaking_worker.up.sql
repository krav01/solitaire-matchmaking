ALTER TABLE matchmaking_tickets
    ADD COLUMN next_attempt_at timestamptz,
    ADD COLUMN claim_token text,
    ADD COLUMN claimed_until timestamptz,
    ADD COLUMN attempt_count bigint NOT NULL DEFAULT 0,
    ADD CONSTRAINT matchmaking_tickets_attempt_count_non_negative
        CHECK (attempt_count >= 0),
    ADD CONSTRAINT matchmaking_tickets_claim_consistent
        CHECK (
            (claim_token IS NULL AND claimed_until IS NULL)
            OR (claim_token IS NOT NULL AND claim_token <> '' AND claimed_until IS NOT NULL)
        );

UPDATE matchmaking_tickets
SET next_attempt_at = requested_at
WHERE status = 'queued';

ALTER TABLE matchmaking_tickets
    ADD CONSTRAINT matchmaking_tickets_schedule_consistent
        CHECK (
            (status = 'queued' AND next_attempt_at IS NOT NULL)
            OR (
                status <> 'queued'
                AND next_attempt_at IS NULL
                AND claim_token IS NULL
                AND claimed_until IS NULL
            )
        );

DROP INDEX matchmaking_tickets_queue_claim_idx;

CREATE INDEX matchmaking_tickets_queue_claim_idx
    ON matchmaking_tickets (next_attempt_at, requested_at, ticket_id)
    WHERE status = 'queued';

ALTER TABLE ticket_commands
    DROP CONSTRAINT ticket_commands_type_supported,
    DROP CONSTRAINT ticket_commands_result_supported,
    ADD CONSTRAINT ticket_commands_type_supported
        CHECK (command_type IN ('cancel', 'expire')),
    ADD CONSTRAINT ticket_commands_result_supported
        CHECK (
            (command_type = 'cancel' AND resulting_status = 'cancelled')
            OR (command_type = 'expire' AND resulting_status = 'expired')
        );
