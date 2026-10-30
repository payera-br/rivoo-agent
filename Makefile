GO = docker run --rm -v $(CURDIR):/src -w /src -v rivoo-agent-gomod:/go/pkg/mod -v rivoo-agent-gocache:/root/.cache/go-build -e CGO_ENABLED=0 golang:1.25 go

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

build:
	$(GO) build -trimpath -ldflags "-s -w -X main.version=$${VERSION:-dev}" -o dist/rivoo-agent ./cmd/rivoo-agent

.PHONY: test vet build
