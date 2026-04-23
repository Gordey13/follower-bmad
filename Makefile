APP_NAME := follower
CMD_PATH := ./cmd/follower
CTL_APP_NAME := followerctl
CTL_CMD_PATH := ./cmd/followerctl
BUILD_DIR := ./bin
BUILD_OUT := $(BUILD_DIR)/$(APP_NAME)-windows
CTL_BUILD_OUT := $(BUILD_DIR)/$(CTL_APP_NAME)-windows
COMPOSE_CMD := docker compose
DOCKER_IMAGE := follower:latest
DOCKER_LOCAL_ARCH ?= amd64
DOCKER_PLATFORM ?= linux/$(DOCKER_LOCAL_ARCH)
DOCKER_LOCAL_BIN := $(BUILD_DIR)/follower-linux-$(DOCKER_LOCAL_ARCH)
DOCKER_LOCAL_CTL_BIN := $(BUILD_DIR)/$(CTL_APP_NAME)-linux-$(DOCKER_LOCAL_ARCH)

.PHONY: all build build-windows build-linux build-followerctl-windows build-followerctl-linux build-docker test-go test-e2e compose-check compose-up compose-down compose-logs

all: build test

build-windows:
	@mkdir -p $(BUILD_DIR)
	go build -o $(BUILD_OUT) $(CMD_PATH)

build-linux:
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=$(DOCKER_LOCAL_ARCH) go build -trimpath -gcflags="all=-N -l" -o $(DOCKER_LOCAL_BIN) $(CMD_PATH)

build-followerctl-windows:
	@mkdir -p $(BUILD_DIR)
	go build -o $(CTL_BUILD_OUT) $(CTL_CMD_PATH)

build-followerctl-linux:
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=$(DOCKER_LOCAL_ARCH) go build -trimpath -gcflags="all=-N -l" -o $(DOCKER_LOCAL_CTL_BIN) $(CTL_CMD_PATH)

build-docker: build-linux build-followerctl-linux
	docker build --platform $(DOCKER_PLATFORM) --build-arg LOCAL_BINARY=$(DOCKER_LOCAL_BIN) --build-arg LOCAL_CTL_BINARY=$(DOCKER_LOCAL_CTL_BIN) --target final -t $(DOCKER_IMAGE) .

test-go:
	go test ./...

compose-check:
	@docker info > /dev/null 2>&1 || (echo "Docker daemon is not running. Start Docker Desktop and wait until engine is healthy."; exit 1)

compose-up: compose-check
	docker compose up -d

compose-down:
	docker compose down --remove-orphans

compose-logs:
	docker compose logs -f
