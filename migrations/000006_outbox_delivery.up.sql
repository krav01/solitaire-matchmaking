ALTER TABLE outbox_events
    ADD CONSTRAINT outbox_events_delivery_order CHECK (
        delivered_at IS NULL OR delivered_at >= occurred_at
    ),
    ADD CONSTRAINT outbox_events_delivered_unclaimed CHECK (
        delivered_at IS NULL OR (claimed_by IS NULL AND claimed_until IS NULL)
    ),
    ADD CONSTRAINT outbox_events_last_error_bounded CHECK (
        last_error IS NULL OR char_length(last_error) <= 1024
    );

CREATE INDEX outbox_events_aggregate_delivery_idx
    ON outbox_events (aggregate_type, aggregate_id, aggregate_version, event_id)
    WHERE delivered_at IS NULL;
