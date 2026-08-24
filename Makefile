VERSION ?= dev
COMMIT  ?= none
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

GOFLAGS := -trimpath -ldflags "-s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildDate=$(DATE)"
BIN     := relayscope

.PHONY: build run test vet lint fmt tidy clean check

## build: compile the server binary into ./bin/
build:
	CGO_ENABLED=0 go build $(GOFLAGS) -o bin/$(BIN) ./cmd/relayscope

## run: run locally (defaults from internal/config)
run:
	go run ./cmd/relayscope

## test: run Go tests and the embedded frontend tests
test:
	go test ./...
	node --test web/public/dashboard.test.cjs web/admin/admin.test.cjs extension/session-sync/capture.test.cjs

## vet: go vet
vet:
	go vet ./...

## fmt: format all Go sources
fmt:
	gofmt -s -w .

## lint: static analysis (requires golangci-lint installed)
lint:
	golangci-lint run ./...

## check: full pre-commit gate (fmt-check + vet + test)
check: vet test
	@test -z "$$(gofmt -l . | tee /dev/stderr)" || (echo "gofmt needed; run 'make fmt'"; exit 1)

## tidy: go mod tidy
tidy:
	go mod tidy

## clean: remove build artifacts
clean:
	rm -rf bin/ dist/ coverage/
