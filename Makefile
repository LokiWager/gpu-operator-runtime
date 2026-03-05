APP_NAME := runtime

GOFILES := $(shell find . -type f -name '*.go' -not -path './vendor/*')

.PHONY: run run-operator build test test-race vet fmt fmt-check tidy ci

run:
	go run ./cmd/runtime

run-operator:
	go run ./cmd/runtime --mode=operator

build:
	mkdir -p bin
	go build -o bin/$(APP_NAME) ./cmd/runtime

test:
	go test ./...

test-race:
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -w $(GOFILES)

fmt-check:
	@out=$$(gofmt -l $(GOFILES)); \
	if [ -n "$$out" ]; then \
		echo "gofmt check failed for:"; \
		echo "$$out"; \
		exit 1; \
	fi

tidy:
	go mod tidy

ci: fmt-check vet test-race build
