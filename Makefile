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

