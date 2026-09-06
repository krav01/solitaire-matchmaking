ALTER TABLE rooms
    ADD COLUMN deck_version text,
    ADD CONSTRAINT rooms_deck_version_present CHECK (
        deck_version IS NULL OR deck_version <> ''
    );

CREATE TABLE rating_shadow_deployments (
    candidate_version text PRIMARY KEY REFERENCES rating_models(model_version),
    baseline_version text NOT NULL REFERENCES rating_models(model_version),
    mode_id text NOT NULL,
    scoring_rules_version text NOT NULL,
    deck_version text NOT NULL,
    training_cutoff timestamptz NOT NULL,
    trained_through timestamptz NOT NULL,
    definition jsonb NOT NULL,
    definition_digest text NOT NULL,
    activated_at timestamptz NOT NULL,
    ended_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT rating_shadow_deployments_versions_distinct CHECK (
        candidate_version <> baseline_version
    ),
    CONSTRAINT rating_shadow_deployments_context_present CHECK (
        mode_id <> '' AND scoring_rules_version <> '' AND deck_version <> ''
    ),
    CONSTRAINT rating_shadow_deployments_definition_object CHECK (
        jsonb_typeof(definition) = 'object'
    ),
    CONSTRAINT rating_shadow_deployments_digest_sha256 CHECK (
        definition_digest ~ '^[0-9a-f]{64}$'
    ),
    CONSTRAINT rating_shadow_deployments_training_order CHECK (
        trained_through <= training_cutoff
        AND training_cutoff < activated_at
        AND activated_at >= created_at
    ),
    CONSTRAINT rating_shadow_deployments_lifetime CHECK (
        ended_at IS NULL OR ended_at > activated_at
    )
);

CREATE UNIQUE INDEX rating_shadow_deployments_active_context_idx
    ON rating_shadow_deployments (mode_id, scoring_rules_version, deck_version)
    WHERE ended_at IS NULL;

CREATE FUNCTION protect_rating_shadow_deployment()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'rating shadow deployments are append-only';
    END IF;
    IF NEW.candidate_version IS DISTINCT FROM OLD.candidate_version
        OR NEW.baseline_version IS DISTINCT FROM OLD.baseline_version
        OR NEW.mode_id IS DISTINCT FROM OLD.mode_id
        OR NEW.scoring_rules_version IS DISTINCT FROM OLD.scoring_rules_version
        OR NEW.deck_version IS DISTINCT FROM OLD.deck_version
        OR NEW.training_cutoff IS DISTINCT FROM OLD.training_cutoff
        OR NEW.trained_through IS DISTINCT FROM OLD.trained_through
        OR NEW.definition IS DISTINCT FROM OLD.definition
        OR NEW.definition_digest IS DISTINCT FROM OLD.definition_digest
        OR NEW.activated_at IS DISTINCT FROM OLD.activated_at
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
        OR (OLD.ended_at IS NOT NULL AND NEW.ended_at IS DISTINCT FROM OLD.ended_at)
    THEN
        RAISE EXCEPTION 'rating shadow deployment definition is immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER rating_shadow_deployments_append_only
BEFORE UPDATE OR DELETE ON rating_shadow_deployments
FOR EACH ROW EXECUTE FUNCTION protect_rating_shadow_deployment();

CREATE TABLE rating_shadow_work (
    work_kind text NOT NULL,
    source_id text NOT NULL,
    timeline_position timestamptz NOT NULL,
    ordering_priority smallint NOT NULL,
    next_attempt_at timestamptz NOT NULL,
    claim_token text,
    claimed_until timestamptz,
    attempt_count integer NOT NULL DEFAULT 0,
    processed_at timestamptz,
    skip_reason text,
    PRIMARY KEY (work_kind, source_id),
    CONSTRAINT rating_shadow_work_kind_supported CHECK (
        (work_kind = 'room' AND ordering_priority = 0)
        OR (work_kind = 'result' AND ordering_priority = 1)
    ),
    CONSTRAINT rating_shadow_work_source_present CHECK (source_id <> ''),
    CONSTRAINT rating_shadow_work_attempts_non_negative CHECK (attempt_count >= 0),
    CONSTRAINT rating_shadow_work_claim_pair CHECK (
        (claim_token IS NULL AND claimed_until IS NULL)
        OR (claim_token IS NOT NULL AND claim_token <> '' AND claimed_until IS NOT NULL)
    ),
    CONSTRAINT rating_shadow_work_state CHECK (
        (processed_at IS NULL AND skip_reason IS NULL)
        OR (processed_at IS NOT NULL AND claim_token IS NULL AND claimed_until IS NULL)
    ),
    CONSTRAINT rating_shadow_work_processing_order CHECK (
        processed_at IS NULL OR processed_at >= timeline_position
    )
);

CREATE INDEX rating_shadow_work_head_idx
    ON rating_shadow_work (timeline_position, ordering_priority, source_id)
    WHERE processed_at IS NULL;

CREATE TABLE rating_shadow_predictions (
    room_id text NOT NULL REFERENCES rooms(room_id),
    candidate_version text NOT NULL REFERENCES rating_shadow_deployments(candidate_version),
    baseline_version text NOT NULL REFERENCES rating_models(model_version),
    segment_id text NOT NULL,
    generated_at timestamptz NOT NULL,
    baseline_prediction jsonb NOT NULL,
    candidate_prediction jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (room_id, candidate_version),
    UNIQUE (room_id),
    CONSTRAINT rating_shadow_predictions_segment_present CHECK (segment_id <> ''),
    CONSTRAINT rating_shadow_predictions_payloads_object CHECK (
        jsonb_typeof(baseline_prediction) = 'object'
        AND jsonb_typeof(candidate_prediction) = 'object'
    )
);

CREATE TABLE rating_shadow_player_states (
    player_id text NOT NULL,
    mode_id text NOT NULL,
    candidate_version text NOT NULL REFERENCES rating_shadow_deployments(candidate_version),
    mean double precision NOT NULL,
    uncertainty double precision NOT NULL,
    performance_deviation double precision,
    games bigint NOT NULL,
    updated_at timestamptz NOT NULL,
    revision bigint NOT NULL,
    feature_profile jsonb NOT NULL,
    PRIMARY KEY (player_id, mode_id, candidate_version),
    CONSTRAINT rating_shadow_player_states_identity_present CHECK (
        player_id <> '' AND mode_id <> ''
    ),
    CONSTRAINT rating_shadow_player_states_mean_finite CHECK (
        mean > '-Infinity'::double precision AND mean < 'Infinity'::double precision
    ),
    CONSTRAINT rating_shadow_player_states_uncertainty_positive CHECK (
        uncertainty > 0 AND uncertainty < 'Infinity'::double precision
    ),
    CONSTRAINT rating_shadow_player_states_deviation_valid CHECK (
        performance_deviation IS NULL
        OR (performance_deviation >= 0 AND performance_deviation < 'Infinity'::double precision)
    ),
    CONSTRAINT rating_shadow_player_states_counters_non_negative CHECK (
        games >= 0 AND revision >= 0
    ),
    CONSTRAINT rating_shadow_player_states_profile_object CHECK (
        jsonb_typeof(feature_profile) = 'object'
    )
);

CREATE TABLE rating_shadow_updates (
    player_id text NOT NULL,
    mode_id text NOT NULL,
    source_event_id text NOT NULL REFERENCES verified_results(event_id),
    candidate_version text NOT NULL REFERENCES rating_shadow_deployments(candidate_version),
    before_estimate jsonb NOT NULL,
    after_estimate jsonb NOT NULL,
    feature_profile jsonb NOT NULL,
    processed_at timestamptz NOT NULL,
    PRIMARY KEY (player_id, source_event_id, candidate_version),
    FOREIGN KEY (source_event_id, player_id)
        REFERENCES verified_result_participants(event_id, player_id),
    CONSTRAINT rating_shadow_updates_payloads_object CHECK (
        jsonb_typeof(before_estimate) = 'object'
        AND jsonb_typeof(after_estimate) = 'object'
        AND jsonb_typeof(feature_profile) = 'object'
    )
);

CREATE TABLE rating_shadow_observations (
    source_event_id text NOT NULL REFERENCES verified_results(event_id),
    room_id text NOT NULL REFERENCES rooms(room_id),
    candidate_version text NOT NULL REFERENCES rating_shadow_deployments(candidate_version),
    baseline_version text NOT NULL REFERENCES rating_models(model_version),
    segment_id text NOT NULL,
    result_available_at timestamptz NOT NULL,
    baseline_brier double precision NOT NULL,
    baseline_log_loss double precision NOT NULL,
    candidate_brier double precision NOT NULL,
    candidate_log_loss double precision NOT NULL,
    evaluated_at timestamptz NOT NULL,
    PRIMARY KEY (source_event_id, candidate_version),
    UNIQUE (room_id, candidate_version),
    CONSTRAINT rating_shadow_observations_segment_present CHECK (segment_id <> ''),
    CONSTRAINT rating_shadow_observations_metrics_finite CHECK (
        baseline_brier >= 0 AND baseline_brier < 'Infinity'::double precision
        AND baseline_log_loss >= 0 AND baseline_log_loss < 'Infinity'::double precision
        AND candidate_brier >= 0 AND candidate_brier < 'Infinity'::double precision
        AND candidate_log_loss >= 0 AND candidate_log_loss < 'Infinity'::double precision
    ),
    CONSTRAINT rating_shadow_observations_time_order CHECK (
        evaluated_at >= result_available_at
    )
);

CREATE INDEX rating_shadow_observations_comparison_idx
    ON rating_shadow_observations (
        candidate_version, segment_id, result_available_at, source_event_id
    );
