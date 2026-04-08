APP_NAME := follower
CMD_PATH := ./cmd/follower
BUILD_DIR := ./bin
BUILD_OUT := $(BUILD_DIR)/$(APP_NAME)-windows
COMPOSE_CMD := docker compose
DOCKER_IMAGE := follower:latest
DOCKER_LOCAL_ARCH ?= amd64
DOCKER_PLATFORM ?= linux/$(DOCKER_LOCAL_ARCH)
DOCKER_LOCAL_BIN := $(BUILD_DIR)/follower-linux-$(DOCKER_LOCAL_ARCH)

.PHONY: all build build-windows build-linux build-docker test-go test-e2e compose-check compose-up compose-down compose-logs

all: build test

build-windows:
	@mkdir -p $(BUILD_DIR)
	go build -o $(BUILD_OUT) $(CMD_PATH)

build-linux:
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=$(DOCKER_LOCAL_ARCH) go build -trimpath -ldflags="-s -w" -o $(DOCKER_LOCAL_BIN) $(CMD_PATH)

build-docker: build-linux
	docker build --platform $(DOCKER_PLATFORM) --build-arg LOCAL_BINARY=$(DOCKER_LOCAL_BIN) --target final -t $(DOCKER_IMAGE) .

debian-base: compose-check
	docker build --network=host -f Dockerfile.base -t debian-base .

node-base: compose-check
	docker build --network=host -f Dockerfile.node-base -t node-base .

docker-build-am: debian-base
	docker build --network=host -f Dockerfile.agent-mail -t mcp-agent-mail .

docker-build-opencode: node-base
	docker build --network=host -f Dockerfile.opencode -t opencode .

test-go:
	go test ./...

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

compose-check:
	@docker info > /dev/null 2>&1 || (echo "Docker daemon is not running. Start Docker Desktop and wait until engine is healthy."; exit 1)

compose-up: compose-check
	docker compose up -d

compose-down:
	docker compose down --remove-orphans

compose-logs:
	docker compose logs -f
