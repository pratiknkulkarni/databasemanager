BINARY_NAME=databasemanager
GO_FILES=$(shell find . -name '*.go')

.PHONY: all build test clean

all: test build

build:
	@echo "Building $(BINARY_NAME)..."
	@go build -o $(BINARY_NAME) main.go
	@echo "Build complete."

test:
	@echo "Running tests..."
	@go test ./...
	@echo "Tests passed."

clean:
	@echo "Cleaning up..."
	@rm -f $(BINARY_NAME)
	@echo "Clean complete."
