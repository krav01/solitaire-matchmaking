GO ?= go
GOLANGCI_LINT ?= golangci-lint

.PHONY: build test race lint fmt tidy check security integration migrate

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
	$(GO) test -count=1 -run '^TestMigrationsApplyToPostgreSQL$$' ./internal/postgres

migrate:
	$(GO) run ./cmd/migrate
