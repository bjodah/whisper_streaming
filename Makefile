.PHONY: build test lint clean run

APP_NAME = whisper-proxy
BUILD_DIR = bin

build:
	@echo "Building $(APP_NAME)..."
	@mkdir -p $(BUILD_DIR)
	@go build -o $(BUILD_DIR)/$(APP_NAME) ./cmd/whisper-proxy

test:
	@echo "Running tests..."
	@go test -v -race ./...

lint:
	@echo "Running linter..."
	@if command -v golangci-lint >/dev/null; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not installed. Run: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; \
	fi

clean:
	@echo "Cleaning up..."
	@rm -rf $(BUILD_DIR)

run: build
	@./$(BUILD_DIR)/$(APP_NAME)
