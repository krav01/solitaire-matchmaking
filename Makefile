GO ?= go
DOCKER ?= docker
GOLANGCI_LINT_VERSION ?= v2.13.2
GOLANGCI_LINT ?= $(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
RELEASE_IMAGE ?= solitaire-matchmaking:release-check

.PHONY: build test race lint fmt tidy check security integration canary container release-check migrate backup-restore-rehearsal

build:
	$(GO) build ./...

test:
	$(GO) test -shuffle=on ./...

race:
	$(GO) test -race -shuffle=on ./...

lint:
	$(GOLANGCI_LINT) run ./...

fmt:
	$(GOLANGCI_LINT) fmt ./...

tidy:
	$(GO) mod tidy

check: tidy build race lint
	git diff --exit-code -- go.mod go.sum

security:
	$(GO) run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...

integration:
	@test -n "$$TEST_DATABASE_URL" || { echo "TEST_DATABASE_URL is required" >&2; exit 1; }
	$(GO) test -count=1 -run '^Test(MigrationsApplyToPostgreSQL|OutboxDeliveryPostgreSQL|TicketLifecyclePostgreSQL|MatchmakingWorkerPostgreSQL|ResultFinalizationPostgreSQL|RatingWorkerPostgreSQL|RatingShadowPostgreSQL.*|TournamentLifecyclePostgreSQLEndToEnd|OutboxResiliencePostgreSQL)$$' ./internal/postgres

canary:
	@test -n "$$CANARY_DATABASE_URL" || { echo "CANARY_DATABASE_URL is required" >&2; exit 1; }
	CANARY_RUN=1 $(GO) test -count=1 -run '^TestCanaryLifecycleWithGameBackend$$' ./examples/game-backend

container:
	$(DOCKER) build --tag $(RELEASE_IMAGE) .
	test "$$($(DOCKER) image inspect --format '{{.Config.User}}' $(RELEASE_IMAGE))" = "65532:65532"

release-check: check security integration canary container

migrate:
	$(GO) run ./cmd/migrate

backup-restore-rehearsal:
	GO=$(GO) bash scripts/rehearse-backup-restore.sh
