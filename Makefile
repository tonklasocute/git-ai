BINARY  := git-quill
PKG     := github.com/tonklasocute/git-quill
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X $(PKG)/internal/cli.version=$(VERSION)

.PHONY: build install test race cover lint fmt tapes snapshot clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/git-quill

install:
	CGO_ENABLED=0 go install -trimpath -ldflags "$(LDFLAGS)" ./cmd/git-quill

test:
	go test ./...

race:
	go test -race ./...

cover:
	go test -race -coverpkg=./internal/... -coverprofile=coverage.out ./...
	@go tool cover -func=coverage.out | tail -1

lint:
	golangci-lint run ./...

fmt:
	gofmt -w .

# Renders docs/tapes/*.tape into docs/gifs/*.gif with the fake provider.
tapes: build
	@for t in docs/tapes/*.tape; do echo "vhs $$t"; PATH="$(CURDIR):$$PATH" vhs $$t || exit 1; done

snapshot:
	goreleaser release --snapshot --clean

clean:
	rm -rf $(BINARY) dist coverage.out coverage.html
