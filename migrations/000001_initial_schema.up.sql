CREATE TABLE rating_models (
    model_version text PRIMARY KEY,
    feature_schema jsonb NOT NULL DEFAULT '{}'::jsonb,
    parameters_digest text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT rating_models_version_present CHECK (model_version <> ''),
    CONSTRAINT rating_models_schema_object CHECK (jsonb_typeof(feature_schema) = 'object'),
    CONSTRAINT rating_models_digest_present CHECK (parameters_digest <> '')
);

CREATE TABLE matching_policies (
    policy_version text PRIMARY KEY,
    rating_model_version text NOT NULL REFERENCES rating_models(model_version),
    definition jsonb NOT NULL,
    definition_digest text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT matching_policies_version_present CHECK (policy_version <> ''),
    CONSTRAINT matching_policies_definition_object CHECK (jsonb_typeof(definition) = 'object'),
    CONSTRAINT matching_policies_digest_present CHECK (definition_digest <> ''),
    UNIQUE (policy_version, rating_model_version)
);

CREATE TABLE tournament_configs (
    tournament_id text NOT NULL,
    version text NOT NULL,
    mode_id text NOT NULL,
    capacity smallint NOT NULL,
    entry_fee_minor bigint NOT NULL,
    currency text NOT NULL,
    scoring_rules_version text NOT NULL,
    settlement_version text NOT NULL,
    policy_version text NOT NULL,
    rating_model_version text NOT NULL,
    result_timeout_ms bigint NOT NULL,
    active_from timestamptz,
    active_until timestamptz,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tournament_id, version),
    FOREIGN KEY (policy_version, rating_model_version)
        REFERENCES matching_policies(policy_version, rating_model_version),
    CONSTRAINT tournament_configs_identity_present CHECK (
        tournament_id <> '' AND version <> '' AND mode_id <> ''
    ),
    CONSTRAINT tournament_configs_capacity_supported CHECK (capacity IN (5, 6, 7)),
    CONSTRAINT tournament_configs_fee_non_negative CHECK (entry_fee_minor >= 0),
    CONSTRAINT tournament_configs_currency_present CHECK (currency <> ''),
    CONSTRAINT tournament_configs_rules_present CHECK (
        scoring_rules_version <> '' AND settlement_version <> ''
    ),
    CONSTRAINT tournament_configs_timeout_positive CHECK (result_timeout_ms > 0),
    CONSTRAINT tournament_configs_activation_order CHECK (
        active_until IS NULL OR active_from IS NULL OR active_until > active_from
    ),
    UNIQUE (tournament_id, version, rating_model_version),
    UNIQUE (
        tournament_id,
        version,
        mode_id,
        capacity,
        scoring_rules_version,
        settlement_version,
        policy_version,
        rating_model_version
    )
);

CREATE INDEX tournament_configs_active_lookup_idx
    ON tournament_configs (tournament_id, active_from, active_until);

CREATE TABLE matchmaking_tickets (
    ticket_id text PRIMARY KEY,
    entry_id text NOT NULL UNIQUE,
    request_digest text NOT NULL,
    player_id text NOT NULL,
    tournament_id text NOT NULL,
    tournament_version text NOT NULL,
    status text NOT NULL,
    requested_at timestamptz NOT NULL,
    assigned_at timestamptz,
    cancelled_at timestamptz,
    expired_at timestamptz,
    snapshot_at timestamptz NOT NULL,
    rating_mean double precision NOT NULL,
    rating_uncertainty double precision NOT NULL,
    rating_performance_deviation double precision,
    rating_games bigint NOT NULL,
    rating_model_version text NOT NULL,
    rating_updated_at timestamptz NOT NULL,
    FOREIGN KEY (tournament_id, tournament_version, rating_model_version)
        REFERENCES tournament_configs(tournament_id, version, rating_model_version),
    CONSTRAINT matchmaking_tickets_identity_present CHECK (
        ticket_id <> '' AND entry_id <> '' AND request_digest <> '' AND player_id <> ''
    ),
    CONSTRAINT matchmaking_tickets_status_supported CHECK (
        status IN ('queued', 'assigned', 'cancelled', 'expired')
    ),
    CONSTRAINT matchmaking_tickets_rating_mean_finite CHECK (
        rating_mean > '-Infinity'::double precision
        AND rating_mean < 'Infinity'::double precision
    ),
    CONSTRAINT matchmaking_tickets_uncertainty_positive CHECK (
        rating_uncertainty > 0
        AND rating_uncertainty < 'Infinity'::double precision
    ),
    CONSTRAINT matchmaking_tickets_deviation_valid CHECK (
        rating_performance_deviation IS NULL
        OR (
            rating_performance_deviation >= 0
            AND rating_performance_deviation < 'Infinity'::double precision
        )
    ),
    CONSTRAINT matchmaking_tickets_games_non_negative CHECK (rating_games >= 0),
    CONSTRAINT matchmaking_tickets_snapshot_available CHECK (
        rating_updated_at <= snapshot_at AND snapshot_at <= requested_at
    ),
    CONSTRAINT matchmaking_tickets_state_timestamps CHECK (
        (status = 'queued' AND assigned_at IS NULL AND cancelled_at IS NULL AND expired_at IS NULL)
        OR (status = 'assigned' AND assigned_at IS NOT NULL AND cancelled_at IS NULL AND expired_at IS NULL)
        OR (status = 'cancelled' AND assigned_at IS NULL AND cancelled_at IS NOT NULL AND expired_at IS NULL)
        OR (status = 'expired' AND assigned_at IS NULL AND cancelled_at IS NULL AND expired_at IS NOT NULL)
    ),
    UNIQUE (ticket_id, player_id)
);

CREATE INDEX matchmaking_tickets_queue_claim_idx
    ON matchmaking_tickets (tournament_id, tournament_version, requested_at, ticket_id)
    WHERE status = 'queued';

CREATE TABLE rooms (
    room_id text PRIMARY KEY,
    tournament_id text NOT NULL,
    tournament_version text NOT NULL,
    mode_id text NOT NULL,
    policy_version text NOT NULL,
    rating_model_version text NOT NULL,
    scoring_rules_version text NOT NULL,
    settlement_version text NOT NULL,
    deck_id text NOT NULL,
    capacity smallint NOT NULL,
    status text NOT NULL,
    aggregate_version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL,
    fill_deadline timestamptz NOT NULL,
    filled_at timestamptz,
    result_deadline timestamptz,
    completed_at timestamptz,
    expired_at timestamptz,
    cancelled_at timestamptz,
    FOREIGN KEY (
        tournament_id,
        tournament_version,
        mode_id,
        capacity,
        scoring_rules_version,
        settlement_version,
        policy_version,
        rating_model_version
    ) REFERENCES tournament_configs (
        tournament_id,
        version,
        mode_id,
        capacity,
        scoring_rules_version,
        settlement_version,
        policy_version,
        rating_model_version
    ),
    CONSTRAINT rooms_identity_present CHECK (room_id <> '' AND deck_id <> ''),
    CONSTRAINT rooms_capacity_supported CHECK (capacity IN (5, 6, 7)),
    CONSTRAINT rooms_status_supported CHECK (
        status IN ('forming', 'collecting', 'completed', 'expired', 'cancelled')
    ),
    CONSTRAINT rooms_version_positive CHECK (aggregate_version > 0),
    CONSTRAINT rooms_fill_deadline_order CHECK (fill_deadline > created_at),
    CONSTRAINT rooms_filled_order CHECK (
        filled_at IS NULL OR (filled_at >= created_at AND filled_at <= fill_deadline)
    ),
    CONSTRAINT rooms_result_deadline_order CHECK (
        result_deadline IS NULL OR (filled_at IS NOT NULL AND result_deadline > filled_at)
    ),
    CONSTRAINT rooms_completed_order CHECK (
        completed_at IS NULL OR (filled_at IS NOT NULL AND completed_at >= filled_at)
    ),
    CONSTRAINT rooms_state_timestamps CHECK (
        (status = 'forming' AND filled_at IS NULL AND result_deadline IS NULL AND completed_at IS NULL)
        OR (status = 'collecting' AND filled_at IS NOT NULL AND result_deadline IS NOT NULL AND completed_at IS NULL)
        OR (status = 'completed' AND filled_at IS NOT NULL AND result_deadline IS NOT NULL AND completed_at IS NOT NULL)
        OR (status = 'expired' AND completed_at IS NULL AND expired_at IS NOT NULL)
        OR (status = 'cancelled' AND completed_at IS NULL AND cancelled_at IS NOT NULL)
    ),
    UNIQUE (room_id, capacity),
    UNIQUE (room_id, mode_id, deck_id, scoring_rules_version)
);

CREATE INDEX rooms_forming_claim_idx
    ON rooms (tournament_id, tournament_version, created_at, room_id)
    WHERE status = 'forming';

CREATE INDEX rooms_result_deadline_idx
    ON rooms (result_deadline, room_id)
    WHERE status = 'collecting';

CREATE TABLE room_memberships (
    room_id text NOT NULL,
    room_capacity smallint NOT NULL,
    ticket_id text NOT NULL UNIQUE,
    player_id text NOT NULL,
    seat smallint NOT NULL,
    assigned_at timestamptz NOT NULL,
    PRIMARY KEY (room_id, seat),
    FOREIGN KEY (room_id, room_capacity) REFERENCES rooms(room_id, capacity),
    FOREIGN KEY (ticket_id, player_id) REFERENCES matchmaking_tickets(ticket_id, player_id),
    CONSTRAINT room_memberships_seat_bounded CHECK (
        seat >= 1 AND seat <= room_capacity
    ),
    UNIQUE (room_id, player_id),
    UNIQUE (room_id, seat, ticket_id, player_id)
);

CREATE TABLE sessions (
    session_id text PRIMARY KEY,
    ticket_id text NOT NULL UNIQUE,
    room_id text NOT NULL,
    player_id text NOT NULL,
    seat smallint NOT NULL,
    status text NOT NULL,
    allocated_at timestamptz NOT NULL,
    started_at timestamptz,
    submitted_at timestamptz,
    forfeited_at timestamptz,
    FOREIGN KEY (room_id, seat, ticket_id, player_id)
        REFERENCES room_memberships(room_id, seat, ticket_id, player_id),
    CONSTRAINT sessions_identity_present CHECK (session_id <> ''),
    CONSTRAINT sessions_status_supported CHECK (
        status IN ('allocated', 'playing', 'submitted', 'forfeited')
    ),
    CONSTRAINT sessions_state_timestamps CHECK (
        (status = 'allocated' AND started_at IS NULL AND submitted_at IS NULL AND forfeited_at IS NULL)
        OR (status = 'playing' AND started_at IS NOT NULL AND submitted_at IS NULL AND forfeited_at IS NULL)
        OR (status = 'submitted' AND started_at IS NOT NULL AND submitted_at IS NOT NULL AND forfeited_at IS NULL)
        OR (status = 'forfeited' AND submitted_at IS NULL AND forfeited_at IS NOT NULL)
    ),
    UNIQUE (session_id, room_id, player_id)
);

CREATE INDEX sessions_room_idx ON sessions (room_id, seat);

CREATE TABLE verified_results (
    event_id text PRIMARY KEY,
    request_digest text NOT NULL,
    room_id text NOT NULL UNIQUE,
    room_capacity smallint NOT NULL,
    mode_id text NOT NULL,
    deck_id text NOT NULL,
    scoring_rules_version text NOT NULL,
    finished_at timestamptz NOT NULL,
    available_at timestamptz NOT NULL,
    accepted_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    processed_at timestamptz,
    FOREIGN KEY (room_id, room_capacity) REFERENCES rooms(room_id, capacity),
    FOREIGN KEY (room_id, mode_id, deck_id, scoring_rules_version)
        REFERENCES rooms(room_id, mode_id, deck_id, scoring_rules_version),
    CONSTRAINT verified_results_identity_present CHECK (
        event_id <> '' AND request_digest <> '' AND mode_id <> ''
    ),
    CONSTRAINT verified_results_availability_order CHECK (available_at >= finished_at),
    CONSTRAINT verified_results_processing_order CHECK (
        processed_at IS NULL OR processed_at >= available_at
    ),
    UNIQUE (event_id, room_id, room_capacity),
    UNIQUE (event_id, mode_id)
);

CREATE INDEX verified_results_processing_idx
    ON verified_results (available_at, event_id)
    WHERE processed_at IS NULL;

CREATE TABLE verified_result_participants (
    event_id text NOT NULL,
    room_id text NOT NULL,
    room_capacity smallint NOT NULL,
    session_id text NOT NULL UNIQUE,
    player_id text NOT NULL,
    place smallint NOT NULL,
    score bigint,
    elapsed_ms bigint,
    completed boolean,
    moves bigint,
    undo_moves bigint,
    revealed_cards bigint,
    PRIMARY KEY (event_id, player_id),
    FOREIGN KEY (event_id, room_id, room_capacity)
        REFERENCES verified_results(event_id, room_id, room_capacity),
    FOREIGN KEY (session_id, room_id, player_id)
        REFERENCES sessions(session_id, room_id, player_id),
    CONSTRAINT verified_result_participants_place_bounded CHECK (
        place >= 1 AND place <= room_capacity
    ),
    CONSTRAINT verified_result_participants_features_non_negative CHECK (
        (elapsed_ms IS NULL OR elapsed_ms >= 0)
        AND (moves IS NULL OR moves >= 0)
        AND (undo_moves IS NULL OR undo_moves >= 0)
        AND (revealed_cards IS NULL OR revealed_cards >= 0)
    )
);

CREATE TABLE player_ratings (
    player_id text NOT NULL,
    mode_id text NOT NULL,
    model_version text NOT NULL REFERENCES rating_models(model_version),
    mean double precision NOT NULL,
    uncertainty double precision NOT NULL,
    performance_deviation double precision,
    games bigint NOT NULL,
    updated_at timestamptz NOT NULL,
    revision bigint NOT NULL,
    PRIMARY KEY (player_id, mode_id, model_version),
    CONSTRAINT player_ratings_identity_present CHECK (player_id <> '' AND mode_id <> ''),
    CONSTRAINT player_ratings_mean_finite CHECK (
        mean > '-Infinity'::double precision AND mean < 'Infinity'::double precision
    ),
    CONSTRAINT player_ratings_uncertainty_positive CHECK (
        uncertainty > 0 AND uncertainty < 'Infinity'::double precision
    ),
    CONSTRAINT player_ratings_deviation_valid CHECK (
        performance_deviation IS NULL
        OR (performance_deviation >= 0 AND performance_deviation < 'Infinity'::double precision)
    ),
    CONSTRAINT player_ratings_counters_non_negative CHECK (games >= 0 AND revision >= 0)
);

CREATE TABLE rating_updates (
    update_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    player_id text NOT NULL,
    mode_id text NOT NULL,
    source_event_id text NOT NULL REFERENCES verified_results(event_id),
    model_version text NOT NULL REFERENCES rating_models(model_version),
    before_mean double precision NOT NULL,
    before_uncertainty double precision NOT NULL,
    before_performance_deviation double precision,
    before_games bigint NOT NULL,
    before_updated_at timestamptz NOT NULL,
    after_mean double precision NOT NULL,
    after_uncertainty double precision NOT NULL,
    after_performance_deviation double precision,
    after_games bigint NOT NULL,
    after_updated_at timestamptz NOT NULL,
    processed_at timestamptz NOT NULL,
    FOREIGN KEY (source_event_id, player_id)
        REFERENCES verified_result_participants(event_id, player_id),
    FOREIGN KEY (source_event_id, mode_id)
        REFERENCES verified_results(event_id, mode_id),
    UNIQUE (player_id, source_event_id, model_version),
    CONSTRAINT rating_updates_means_finite CHECK (
        before_mean > '-Infinity'::double precision
        AND before_mean < 'Infinity'::double precision
        AND after_mean > '-Infinity'::double precision
        AND after_mean < 'Infinity'::double precision
    ),
    CONSTRAINT rating_updates_uncertainty_positive CHECK (
        before_uncertainty > 0
        AND before_uncertainty < 'Infinity'::double precision
        AND after_uncertainty > 0
        AND after_uncertainty < 'Infinity'::double precision
    ),
    CONSTRAINT rating_updates_deviation_valid CHECK (
        (before_performance_deviation IS NULL OR (
            before_performance_deviation >= 0
            AND before_performance_deviation < 'Infinity'::double precision
        ))
        AND (after_performance_deviation IS NULL OR (
            after_performance_deviation >= 0
            AND after_performance_deviation < 'Infinity'::double precision
        ))
    ),
    CONSTRAINT rating_updates_games_advance CHECK (
        before_games >= 0 AND after_games = before_games + 1
    ),
    CONSTRAINT rating_updates_time_order CHECK (
        after_updated_at = processed_at AND processed_at >= before_updated_at
    )
);

CREATE INDEX rating_updates_player_history_idx
    ON rating_updates (player_id, mode_id, model_version, processed_at DESC);

CREATE TABLE outbox_events (
    event_id text PRIMARY KEY,
    aggregate_type text NOT NULL,
    aggregate_id text NOT NULL,
    aggregate_version bigint NOT NULL,
    event_type text NOT NULL,
    payload jsonb NOT NULL,
    occurred_at timestamptz NOT NULL,
    available_at timestamptz NOT NULL,
    attempt_count integer NOT NULL DEFAULT 0,
    claimed_by text,
    claimed_until timestamptz,
    delivered_at timestamptz,
    last_error text,
    CONSTRAINT outbox_events_identity_present CHECK (
        event_id <> '' AND aggregate_type <> '' AND aggregate_id <> '' AND event_type <> ''
    ),
    CONSTRAINT outbox_events_versions_non_negative CHECK (
        aggregate_version > 0 AND attempt_count >= 0
    ),
    CONSTRAINT outbox_events_payload_object CHECK (jsonb_typeof(payload) = 'object'),
    CONSTRAINT outbox_events_availability_order CHECK (available_at >= occurred_at),
    CONSTRAINT outbox_events_claim_pair CHECK (
        (claimed_by IS NULL AND claimed_until IS NULL)
        OR (claimed_by IS NOT NULL AND claimed_until IS NOT NULL)
    ),
    UNIQUE (aggregate_type, aggregate_id, aggregate_version, event_type)
);

CREATE INDEX outbox_events_delivery_claim_idx
    ON outbox_events (available_at, occurred_at, event_id)
    WHERE delivered_at IS NULL;

CREATE FUNCTION protect_completed_room_identity()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF OLD.status = 'completed' AND (
        NEW.status IS DISTINCT FROM OLD.status
        OR ROW(
            NEW.tournament_id,
            NEW.tournament_version,
            NEW.mode_id,
            NEW.policy_version,
            NEW.rating_model_version,
            NEW.scoring_rules_version,
            NEW.settlement_version,
            NEW.deck_id,
            NEW.capacity
        ) IS DISTINCT FROM ROW(
            OLD.tournament_id,
            OLD.tournament_version,
            OLD.mode_id,
            OLD.policy_version,
            OLD.rating_model_version,
            OLD.scoring_rules_version,
            OLD.settlement_version,
            OLD.deck_id,
            OLD.capacity
        )
    ) THEN
        RAISE EXCEPTION 'completed room identity is immutable'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER rooms_protect_completed_identity
BEFORE UPDATE ON rooms
FOR EACH ROW
EXECUTE FUNCTION protect_completed_room_identity();

CREATE FUNCTION validate_verified_result_complete()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    checked_event_id text := COALESCE(NEW.event_id, OLD.event_id);
    expected_participants smallint;
    actual_participants bigint;
    winners bigint;
BEGIN
    SELECT room_capacity
    INTO expected_participants
    FROM verified_results
    WHERE event_id = checked_event_id;

    IF NOT FOUND THEN
        RETURN NULL;
    END IF;

    SELECT count(*), count(*) FILTER (WHERE place = 1)
    INTO actual_participants, winners
    FROM verified_result_participants
    WHERE event_id = checked_event_id;

    IF actual_participants <> expected_participants OR winners = 0 THEN
        RAISE EXCEPTION 'verified result % must contain all room participants and a winner', checked_event_id
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER verified_results_require_complete_participants
AFTER INSERT OR UPDATE ON verified_results
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION validate_verified_result_complete();

CREATE CONSTRAINT TRIGGER verified_result_participants_require_complete_result
AFTER INSERT OR UPDATE OR DELETE ON verified_result_participants
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION validate_verified_result_complete();
