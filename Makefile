# Variables
APP_NAME := lspwatch
VERSION := $(shell git describe --tags --always)
BUILD_DIR := build
SRC := $(shell find . -name '*.go' -not -path "./vendor/*")

# Commands
GO := go
GOTEST := $(GO) test
GOBUILD := $(GO) build
GOMOD := $(GO) mod
GOFMT := $(GO) fmt

# Default target
.PHONY: all
all: build

# Build the binary
.PHONY: build
build: clean
	@echo "Building $(APP_NAME)..."
	$(GOBUILD) -o $(BUILD_DIR)/$(APP_NAME) -ldflags "-X main.version=$(VERSION)" .

# Run the application
.PHONY: run
run: build
	@echo "Running $(APP_NAME)..."
	./$(BUILD_DIR)/$(APP_NAME)

# Test the code
.PHONY: test
test:
	@echo "Running tests..."
	$(GOTEST) ./...

# Format the code
.PHONY: fmt
fmt:
	@echo "Formatting code..."
	$(GOFMT) $(SRC)

# Tidy up dependencies
.PHONY: tidy
tidy:
	@echo "Tidying up dependencies..."
	$(GOMOD) tidy

# Clean up generated files
.PHONY: clean
clean:
	@echo "Cleaning up..."
	@rm -rf $(BUILD_DIR)

# Install dependencies
.PHONY: deps
deps:
	@echo "Downloading dependencies..."
	$(GO) mod download

.PHONY: run-otel-collector
run-otel-collector:
	@echo "Running OpenTelemetry collector..."
	docker compose -f otel.compose.yaml up --build 
	# docker pull otel/opentelemetry-collector-contrib
	# docker run -p 127.0.0.1:4318:4318 otel/opentelemetry-collector-contrib 2>&1 | tee collector-output.txt

# Display help
.PHONY: help
help:
	@echo "Available targets:"
	@echo "  all        Build the project"
	@echo "  build      Build the binary"
	@echo "  run        Build and run the project"
	@echo "  test       Run tests"
	@echo "  fmt        Format the code"
	@echo "  tidy       Tidy up dependencies"
	@echo "  clean      Clean up generated files"
	@echo "  deps       Download dependencies"
	@echo "  help       Display this help message"

