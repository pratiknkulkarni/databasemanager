BINARY_NAME := databasemanager

# Prefer the tag if HEAD has one; otherwise the short SHA, with -dirty appended
# when the tree has uncommitted changes. Falls back to "dev" outside a checkout.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

PKG     := github.com/pratiknkulkarni/databasemanager/cmd
LDFLAGS := -s -w \
	-X '$(PKG).version=$(VERSION)' \
	-X '$(PKG).commit=$(COMMIT)' \
	-X '$(PKG).date=$(DATE)'

.PHONY: all check build install test test-race vet fmt tidy clean

all: check build

# What CI runs. Keep it the same set so a green local run means a green CI run.
check: fmt vet test

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY_NAME) .

install:
	go install -trimpath -ldflags "$(LDFLAGS)" .

test:
	go test ./...

test-race:
	go test -race ./...

vet:
	go vet ./...

# Fails rather than rewrites, so a formatting slip cannot pass unnoticed in CI.
fmt:
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt needed:"; echo "$$unformatted"; exit 1; \
	fi

tidy:
	go mod tidy

clean:
	rm -f $(BINARY_NAME)
