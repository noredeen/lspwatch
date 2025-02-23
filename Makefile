APP_NAME := lspwatch
VERSION := $(shell git describe --tags --always)
BUILD_DIR := build
TEST_DATA_DIR := testdata
COVERAGE_DIR := coverage

OTEL_EXPORTS_DIR := /tmp/file-exporter
CONTAINER_ID_FILE := /tmp/$(APP_NAME)-test-container-id

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
	mkdir -p $(COVERAGE_DIR)/unit
	go test -v -cover -covermode=atomic ./... -args -test.gocoverdir="$(PWD)/$(COVERAGE_DIR)/unit"

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
	rm -rf $(BUILD_DIR)

.PHONY: clean-coverage
clean-coverage:
	@echo "Cleaning up coverage files..."
	rm -rf $(COVERAGE_DIR)

.PHONY: clean-integration-runnables
clean-integration-runnables:
	@echo "Cleaning up integration test runnables..."
	@rm -rf ./integration/$(BUILD_DIR)

.PHONY: deps
deps:
	@echo "Downloading dependencies..."
	go mod download

.PHONY: start-otel-collector
start-otel-collector:
	@echo "Starting OpenTelemetry Collector..."
	rm -rf $(OTEL_EXPORTS_DIR)
	mkdir -m 777 $(OTEL_EXPORTS_DIR)
	@container_id=$$(docker run -d -p 4317:4317 -v $(OTEL_EXPORTS_DIR):/file-exporter -v ./integration/otel_config.yaml:/etc/otelcol-contrib/config.yaml otel/opentelemetry-collector-contrib ) && \
	echo "$$container_id" > $(CONTAINER_ID_FILE) && \
	echo "Container started with ID: $$container_id"

.PHONY: build-test
build-test: clean
	@echo "Building lspwatch for testing..."
	go build -cover -o $(BUILD_DIR)/$(APP_NAME)_cov

.PHONY: build-integration-runnables
build-integration-runnables: clean-integration-runnables
	@echo "Building integration runnables..."
	go build -o ./integration/$(BUILD_DIR)/ ./integration/cmd/*.go

.PHONY: set-up-test-dependencies
set-up-test-dependencies: build-test build-integration-runnables start-otel-collector 

.PHONY: stop-otel-collector
stop-otel-collector:
	@echo "Stopping OpenTelemetry Collector..."
	docker container stop $$(cat $(CONTAINER_ID_FILE))
	rm -f $(CONTAINER_ID_FILE)
	rm -rf $(OTEL_EXPORTS_DIR)

.PHONY: tear-down-test-dependencies
tear-down-test-dependencies: stop-otel-collector

.PHONY: integration-tests
integration-tests:
	@echo "Running integration tests..."
	@echo "Unit test coverage files before running integration tests:"
	@ls -la $(COVERAGE_DIR)/unit
	mkdir -p $(COVERAGE_DIR)/int
	TEST_DATA_DIR=$(TEST_DATA_DIR) \
	LSPWATCH_BIN=$(PWD)/$(BUILD_DIR)/$(APP_NAME)_cov \
	COVERAGE_DIR=$(PWD)/$(COVERAGE_DIR)/int \
	go -C integration test -v -covermode=atomic
	@echo "Coverage files after integration tests:"
	@echo "Unit:"
	@ls -la $(COVERAGE_DIR)/unit
	@echo "Integration:"
	@ls -la $(COVERAGE_DIR)/int


.PHONY: ci-integration-tests
ci-integration-tests: set-up-test-dependencies integration-tests tear-down-test-dependencies

.PHONY: combine-coverage
combine-coverage:
	@echo "Combining coverage data..."
	go tool covdata textfmt -i=$(COVERAGE_DIR)/unit,$(COVERAGE_DIR)/int -o=coverage.txt

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

