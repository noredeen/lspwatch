APP_NAME := lspwatch
VERSION := $(shell git describe --tags --always)
BUILD_DIR := build
TEST_DATA_DIR := testdata
COVERAGE_DIR := coverage
INTEGRATION_TEST_DIR := ./internal/integration
UNIT_TEST_DIRS := $(shell go list ./... | grep -v $(INTEGRATION_TEST_DIR))

OTEL_EXPORTS_DIR := /tmp/file-exporter
CONTAINER_ID_FILE := /tmp/$(APP_NAME)-test-container-id
INSTALL_DIR := /usr/local/bin

SRC := $(shell find . -name '*.go' -not -path "./vendor/*")

.PHONY: build
build: clean
	@echo "Building $(APP_NAME)..."
	go build -o $(BUILD_DIR)/$(APP_NAME) -ldflags "-X main.version=$(VERSION)" .

.PHONY: install
install: build
	install -m 755 $(BUILD_DIR)/$(APP_NAME) $(INSTALL_DIR)

.PHONY: uninstall
uninstall:
	rm -f $(INSTALL_DIR)/$(APP_NAME)

.PHONY: run
run: build
	@echo "Running $(APP_NAME)..."
	./$(BUILD_DIR)/$(APP_NAME)

.PHONY: unit-tests
unit-tests:
	@echo "Running unit tests..."
	mkdir -p $(COVERAGE_DIR)/unit
	go test -v -cover -covermode=atomic $(UNIT_TEST_DIRS) -args -test.gocoverdir="$(PWD)/$(COVERAGE_DIR)/unit"

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
	@rm -rf $(INTEGRATION_TEST_DIR)/$(BUILD_DIR)

.PHONY: deps
deps:
	@echo "Downloading dependencies..."
	go mod download

.PHONY: build-test
build-test: clean
	@echo "Building lspwatch for testing..."
	go build -cover -covermode=atomic -o $(BUILD_DIR)/$(APP_NAME)_cov

.PHONY: build-integration-runnables
build-integration-runnables: clean-integration-runnables
	@echo "Building integration runnables..."
	go build -C $(INTEGRATION_TEST_DIR) -o $(BUILD_DIR)/ ./cmd/...

.PHONY: stop-otel-collector
stop-otel-collector:
	@echo "Stopping OpenTelemetry Collector..."
	docker container stop $$(cat $(CONTAINER_ID_FILE))
	rm -f $(CONTAINER_ID_FILE)
	rm -rf $(OTEL_EXPORTS_DIR)

.PHONY: tear-down-test-dependencies
tear-down-test-dependencies: stop-otel-collector

.PHONY: run-integration-tests
run-integration-tests:
	@echo "Running integration tests..."
	mkdir -p $(COVERAGE_DIR)/int
	TEST_DATA_DIR=$(TEST_DATA_DIR) \
	LSPWATCH_BIN=$(PWD)/$(BUILD_DIR)/$(APP_NAME)_cov \
	COVERAGE_DIR=$(PWD)/$(COVERAGE_DIR)/int \
	go test $(INTEGRATION_TEST_DIR)/... -v -cover -covermode=atomic

.PHONY: integration-tests
integration-tests: build-test build-integration-runnables run-integration-tests

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

