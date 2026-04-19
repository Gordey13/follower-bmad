# E2E Profile

This profile runs integration tests against real PostgreSQL and MinIO instances.
It uses the main `docker-compose.yml` (no separate compose file).

## Local run

```bash
make e2e
```

## What it validates

- `internal/repository/postgres` tests with live PostgreSQL (`FOLLOWER_TEST_POSTGRES_URL`)
- `internal/storage` integration tests with live MinIO (`FOLLOWER_TEST_MINIO_*`)

## CI

GitHub Actions workflow: `.github/workflows/e2e.yml`

## Ports

The e2e scripts use standard ports from `docker-compose.yml`:

- PostgreSQL: `5432`
- MinIO API: `9000`
- MinIO Console: `9001`


test-e2e: compose-check
	@set -eu; \
	COMPOSE_FILE="./docker-compose.yml"; \
	cleanup() { docker compose -f "$$COMPOSE_FILE" down --volumes --remove-orphans; }; \
	trap cleanup EXIT INT TERM; \
	docker compose -f "$$COMPOSE_FILE" up -d postgres minio; \
	echo "Waiting for postgres..."; \
	postgres_ready=0; \
	for i in $$(seq 1 90); do \
		if docker compose -f "$$COMPOSE_FILE" exec -T postgres pg_isready -U postgres -d follower_automation >/dev/null 2>&1; then \
			postgres_ready=1; \
			break; \
		fi; \
		sleep 1; \
	done; \
	if [ "$$postgres_ready" -ne 1 ]; then \
		echo "Postgres did not become ready in time" >&2; \
		exit 1; \
	fi; \
	echo "Waiting for minio..."; \
	minio_ready=0; \
	for i in $$(seq 1 90); do \
		if curl -fsS "http://127.0.0.1:9000/minio/health/ready" >/dev/null 2>&1; then \
			minio_ready=1; \
			break; \
		fi; \
		sleep 1; \
	done; \
	if [ "$$minio_ready" -ne 1 ]; then \
		echo "MinIO did not become ready in time" >&2; \
		exit 1; \
	fi; \
	FOLLOWER_TEST_POSTGRES_URL="postgres://postgres:password@127.0.0.1:5432/follower_automation?sslmode=disable" \
	FOLLOWER_TEST_MINIO_ENDPOINT="127.0.0.1:9000" \
	FOLLOWER_TEST_MINIO_ACCESS_KEY="minioadmin" \
	FOLLOWER_TEST_MINIO_SECRET_KEY="minioadmin" \
	FOLLOWER_TEST_MINIO_USE_SSL="false" \
	FOLLOWER_TEST_MINIO_BUCKET="follower-artifacts" \
	go test ./internal/repository/postgres ./internal/storage -count=1 -v
