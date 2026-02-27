APP_NAME := runtime

.PHONY: run build test fmt tidy

run:
	go run ./cmd/runtime

build:
	mkdir -p bin
	go build -o bin/$(APP_NAME) ./cmd/runtime

test:
	go test ./...

fmt:
	gofmt -w ./cmd ./pkg

tidy:
	go mod tidy
