SHELL := /bin/sh

.PHONY: up down run demo build test vet migrate-up clean test-integration test-db

# Bring up db + redis and apply migrations. Then run the API locally:
#   cp .env.example .env  (set DATABASE_URL)  &&  make run
up:
	docker compose up -d db redis
	docker compose run --rm migrate

# Full demo flow (slice 5): db + redis + migrations, seed the multi-role demo
# data (2 tenants x 5 roles, password test123 — see README), then run the API.
# The seed is idempotent, so `make demo` is safe to re-run after demoing.
demo: up
	@test -n "$$DATABASE_URL" || (echo "DATABASE_URL not set; copy .env.example to .env" && exit 1)
	DATABASE_URL="$$DATABASE_URL" go run ./cmd/seed
	DATABASE_URL="$$DATABASE_URL" go run ./cmd/api

# Tear down containers but keep volumes (use down -v to wipe data).
down:
	docker compose down

# Run the API against your local environment (see .env.example).
run:
	@test -n "$$DATABASE_URL" || (echo "DATABASE_URL not set; copy .env.example to .env" && exit 1)
	go run ./cmd/api

build:
	go build -o bin/agro-iam ./cmd/api

test:
	go test ./...

vet:
	go vet ./...

# Run the RLS integration tests against a live postgres. The dedicated
# agroiam_test database must exist (see `make test-db`); the tests create the
# schema themselves, so no migrate container is involved.
# TEST_DATABASE_URL can be overridden on the command line or via the environment.
TEST_DATABASE_URL ?= postgres://agroiam:agroiam@localhost:5432/agroiam_test?sslmode=disable

test-integration:
	TEST_DATABASE_URL="$(TEST_DATABASE_URL)" go test ./internal/infrastructure/postgres/...

# Create the dedicated integration-test database. Idempotent: createdb fails
# when the database already exists, which is fine (|| true).
test-db:
	docker compose exec -T db createdb -U agroiam agroiam_test || true

# Apply pending migrations via the migrate container.
migrate-up:
	docker compose run --rm migrate

clean:
	rm -rf bin
