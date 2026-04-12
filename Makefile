APP_NAME := manager
PROXY_APP_NAME := runtime-proxy
export GOTOOLCHAIN := go1.26.0

GOFILES := $(shell find . -type f -name '*.go' -not -path './vendor/*')
LOCALBIN ?= $(shell pwd)/bin
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
CONTROLLER_TOOLS_VERSION ?= v0.20.1
SWAG ?= $(LOCALBIN)/swag
SWAG_VERSION ?= v1.16.4

.PHONY: run build test test-race vet fmt fmt-check tidy manifests generate swagger controller-gen swag ci

run:
	go run ./cmd/main.go --config config/local/runtime-manager.yaml

build:
	mkdir -p bin
	go build -o bin/$(APP_NAME) ./cmd/main.go
	go build -o bin/$(PROXY_APP_NAME) ./cmd/runtime-proxy

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

swagger: swag
	"$(SWAG)" init -g doc.go -d pkg/api,pkg/service,pkg/contract,pkg/domain,api/v1alpha1 -o docs/swagger --parseDependency --parseInternal

controller-gen:
	@test -s "$(CONTROLLER_GEN)" || { \
		echo "Installing controller-gen $(CONTROLLER_TOOLS_VERSION)"; \
		GOBIN="$(LOCALBIN)" go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION); \
	}

swag:
	@test -s "$(SWAG)" || { \
		echo "Installing swag $(SWAG_VERSION)"; \
		GOBIN="$(LOCALBIN)" go install github.com/swaggo/swag/cmd/swag@$(SWAG_VERSION); \
	}

ci: manifests generate swagger fmt-check vet test-race build
