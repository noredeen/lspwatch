APP_NAME := lspwatch
VERSION := $(shell git describe --tags --always)
BUILD_DIR := build
SRC := $(shell find . -name '*.go' -not -path "./vendor/*")

.PHONY: build
build: clean
	@echo "Building $(APP_NAME)..."
	go build -o $(BUILD_DIR)/$(APP_NAME) -ldflags "-X main.version=$(VERSION)" .

.PHONY: run
run: build
	@echo "Running $(APP_NAME)..."
	./$(BUILD_DIR)/$(APP_NAME)

.PHONY: unit-tests
unit-tests:
	@echo "Running unit tests..."
	go test -v ./...

.PHONY: fmt
fmt:
	@echo "Formatting code..."
	go fmt $(SRC)

.PHONY: tidy
tidy:
	@echo "Tidying up dependencies..."
	go mod tidy

.PHONY: clean
clean:
	@echo "Cleaning up..."
	@rm -rf $(BUILD_DIR)

.PHONY: deps
deps:
	@echo "Downloading dependencies..."
	go mod download

.PHONY: start-otel-collector
start-otel-collector:
	@echo "Starting OpenTelemetry Collector..."
	rm -rf /tmp/file-exporter
	mkdir -m 777 /tmp/file-exporter
	@container_id=$$(docker run -d -p 4317:4317 -v /tmp/file-exporter:/file-exporter -v ./integration/otel_config.yaml:/etc/otelcol-contrib/config.yaml otel/opentelemetry-collector-contrib ) && \
	echo "$$container_id" > /tmp/$(APP_NAME)-test-container-id && \
	echo "Container started with ID: $$container_id"

.PHONY: build-integration-runnables
build-integration-runnables:
	@echo "Building integration runnables..."
	rm -rf ./integration/$(BUILD_DIR)
	go build -o ./integration/$(BUILD_DIR)/ ./integration/cmd/*.go

.PHONY: set-up-test-dependencies
set-up-test-dependencies: build build-integration-runnables start-otel-collector 

.PHONY: stop-otel-collector
stop-otel-collector:
	@echo "Stopping OpenTelemetry Collector..."
	docker container stop $$(cat /tmp/$(APP_NAME)-test-container-id)
	rm -f /tmp/$(APP_NAME)-test-container-id
	rm -rf /tmp/file-exporter

.PHONY: tear-down-test-dependencies
tear-down-test-dependencies: stop-otel-collector clean

.PHONY: integration-tests
integration-tests:
	@echo "Running integration tests..."
	cd integration && go test -v

.PHONY: help
help:
	@echo "Available targets:"
	@echo "  build      Build the binary"
	@echo "  run        Build and run the project"
	@echo "  test       Run tests"
	@echo "  fmt        Format the code"
	@echo "  tidy       Tidy up dependencies"
	@echo "  clean      Clean up generated files"
	@echo "  deps       Download dependencies"
	@echo "  help       Display this help message"

