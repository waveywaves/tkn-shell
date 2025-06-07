.PHONY: build clean test sync-crd

# Build the sync-tekton-crd binary
build:
	@mkdir -p bin
	go build -o bin/sync-tekton-crd ./cmd/sync-tekton-crd

# Clean build artifacts
clean:
	rm -rf bin/

# Test the Go modules
test:
	go test ./...

# Run the sync-tekton-crd command
sync-crd: build
	./bin/sync-tekton-crd

# Install dependencies
deps:
	go mod tidy
	go mod download

# Format code
fmt:
	go fmt ./...

# Lint code (requires golangci-lint)
lint:
	golangci-lint run

# Display help
help:
	@echo "Available targets:"
	@echo "  build     - Build the sync-tekton-crd binary"
	@echo "  clean     - Clean build artifacts"
	@echo "  test      - Run tests"
	@echo "  sync-crd  - Build and run the sync-tekton-crd command"
	@echo "  deps      - Install/update dependencies"
	@echo "  fmt       - Format Go code"
	@echo "  lint      - Lint Go code (requires golangci-lint)"
	@echo "  help      - Display this help message" 