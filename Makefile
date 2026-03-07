APP_NAME := manager
export GOTOOLCHAIN := go1.26.0

GOFILES := $(shell find . -type f -name '*.go' -not -path './vendor/*')
LOCALBIN ?= $(shell pwd)/bin
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
CONTROLLER_TOOLS_VERSION ?= v0.20.1

.PHONY: run build test test-race vet fmt fmt-check tidy manifests generate controller-gen ci

run:
	go run ./cmd/main.go

build:
	mkdir -p bin
	go build -o bin/$(APP_NAME) ./cmd/main.go

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

manifests: controller-gen
	"$(CONTROLLER_GEN)" rbac:roleName=manager-role crd paths="./..." output:crd:artifacts:config=config/crd/bases

generate: controller-gen
	"$(CONTROLLER_GEN)" object:headerFile="hack/boilerplate.go.txt" paths="./..."

controller-gen:
	@test -s "$(CONTROLLER_GEN)" || { \
		echo "Installing controller-gen $(CONTROLLER_TOOLS_VERSION)"; \
		GOBIN="$(LOCALBIN)" go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION); \
	}

ci: manifests generate fmt-check vet test-race build
