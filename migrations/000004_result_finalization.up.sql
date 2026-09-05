ALTER TABLE verified_results
    ADD CONSTRAINT verified_results_request_digest_sha256
        CHECK (request_digest ~ '^[0-9a-f]{64}$');
