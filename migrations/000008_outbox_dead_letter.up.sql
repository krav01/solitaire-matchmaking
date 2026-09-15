ALTER TABLE outbox_events
    ADD COLUMN dead_lettered_at TIMESTAMPTZ,
    ADD COLUMN dead_letter_reason TEXT;

ALTER TABLE outbox_events
    ADD CONSTRAINT outbox_events_dead_letter_pair CHECK (
        (dead_lettered_at IS NULL) = (dead_letter_reason IS NULL)
    ),
    ADD CONSTRAINT outbox_events_dead_letter_reason_bounded CHECK (
        dead_letter_reason IS NULL OR char_length(dead_letter_reason) <= 1024
    ),
    ADD CONSTRAINT outbox_events_dead_letter_terminal CHECK (
        dead_lettered_at IS NULL OR (
            delivered_at IS NULL
            AND claimed_by IS NULL
            AND claimed_until IS NULL
        )
    );
