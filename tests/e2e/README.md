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
