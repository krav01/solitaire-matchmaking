ALTER TABLE verified_results
    ADD COLUMN next_attempt_at timestamptz,
    ADD COLUMN claim_token text,
    ADD COLUMN claimed_until timestamptz,
    ADD COLUMN attempt_count integer NOT NULL DEFAULT 0;

UPDATE verified_results
SET next_attempt_at = available_at
WHERE processed_at IS NULL;

ALTER TABLE verified_results
    ADD CONSTRAINT verified_results_rating_schedule CHECK (
        (processed_at IS NULL AND next_attempt_at IS NOT NULL)
        OR (
            processed_at IS NOT NULL
            AND next_attempt_at IS NULL
            AND claim_token IS NULL
            AND claimed_until IS NULL
        )
    ),
    ADD CONSTRAINT verified_results_rating_claim_pair CHECK (
        (claim_token IS NULL AND claimed_until IS NULL)
        OR (claim_token IS NOT NULL AND claim_token <> '' AND claimed_until IS NOT NULL)
    ),
    ADD CONSTRAINT verified_results_rating_attempts_non_negative CHECK (attempt_count >= 0);

DROP INDEX verified_results_processing_idx;

CREATE INDEX verified_results_rating_head_idx
    ON verified_results (available_at, event_id)
    WHERE processed_at IS NULL;
