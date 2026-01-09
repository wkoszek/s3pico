.PHONY: all build test clean install bench fmt run-server help

BINARY_NAME=s3pico
BUILD_DIR=build
COVERAGE_DIR=coverage

all: build

build:
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	go build -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/s3pico

install:
	@echo "Installing $(BINARY_NAME)..."
	go install ./cmd/s3pico

clean:
	@echo "Cleaning..."
	@rm -rf $(BUILD_DIR) $(COVERAGE_DIR)

test:
	@mkdir -p $(COVERAGE_DIR)
	@echo "=== E2E Tests ==="
	@go test -v -race -coverprofile=$(COVERAGE_DIR)/e2e.out -covermode=atomic -coverpkg=s3pico/pkg/s3pico ./tests/e2e/...
	@echo ""
	@echo "=== AWS CLI Tests ==="
	@go test -v -race -coverprofile=$(COVERAGE_DIR)/awscli.out -covermode=atomic -coverpkg=s3pico/pkg/s3pico ./tests/awscli/...
	@echo ""
	@echo "mode: atomic" > $(COVERAGE_DIR)/coverage.out
	@tail -n +2 $(COVERAGE_DIR)/e2e.out >> $(COVERAGE_DIR)/coverage.out
	@tail -n +2 $(COVERAGE_DIR)/awscli.out >> $(COVERAGE_DIR)/coverage.out
	@go tool cover -html=$(COVERAGE_DIR)/coverage.out -o $(COVERAGE_DIR)/coverage.html
	@echo "=== Coverage Report ==="
	@go tool cover -func=$(COVERAGE_DIR)/coverage.out | tail -1
	@echo ""
	@echo "HTML report: file://$(CURDIR)/$(COVERAGE_DIR)/coverage.html"

bench:
	@echo "Running benchmarks..."
	go test -bench=. -benchmem ./tests/e2e/...
	go test -bench=. -benchmem ./tests/awscli/...

fmt:
	go fmt ./...

run-server:
	go run ./cmd/s3pico server -debug

help:
	@echo "s3pico - Minimal S3-compatible storage server"
	@echo ""
	@echo "Usage:"
	@echo "  make build      - Build the binary"
	@echo "  make install    - Install to GOPATH/bin"
	@echo "  make test       - Run all tests with coverage"
	@echo "  make bench      - Run benchmarks"
	@echo "  make clean      - Remove build artifacts"
	@echo "  make fmt        - Format code"
	@echo "  make run-server - Run server for manual testing"
