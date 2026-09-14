.DEFAULT_GOAL := build

BINARY := skillmod
GO ?= go
GOLANGCI_LINT_VERSION ?= $(shell cat .golangci-version)
GOLANGCI_LINT ?= $(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
GOFMT ?= $(shell $(GO) env GOROOT)/bin/gofmt$(EXE)
COVERAGE_MIN ?= 75.0
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || printf 'dev')
LD_FLAGS ?= -X main.version=$(VERSION)
EXE := $(shell $(GO) env GOEXE)
GOHOSTOS := $(shell $(GO) env GOHOSTOS)
ifeq ($(GOHOSTOS),windows)
PATHSEP := ;
else
PATHSEP := :
endif
INSTALL_DIR ?= $(shell \
	dir="$$($(GO) env GOBIN)"; \
	if [ -z "$$dir" ]; then \
		gopath="$$($(GO) env GOPATH)"; \
		dir="$${gopath%%$(PATHSEP)*}/bin"; \
	fi; \
	printf '%s' "$$dir")

.PHONY: build install test coverage vet lint format-check tidy-check check generate

build:
	$(GO) build -trimpath -ldflags "$(LD_FLAGS)" -o ./$(BINARY)$(EXE) .

install:
	GOBIN="$(INSTALL_DIR)" $(GO) install -trimpath -ldflags "$(LD_FLAGS)" .
	@printf 'installed $(BINARY)$(EXE) to %s\n' "$(INSTALL_DIR)"

test:
	$(GO) test ./...

coverage:
	@profile="$$(mktemp)"; trap 'rm -f "$$profile"' EXIT; \
		$(GO) test -coverprofile="$$profile" ./...; \
		$(GO) tool cover -func="$$profile" | awk -v minimum="$(COVERAGE_MIN)" '/^total:/ { value=$$3; sub(/%$$/, "", value); if (value+0 < minimum+0) { printf "coverage %.1f%% is below %.1f%%\n", value, minimum; exit 1 } printf "coverage %.1f%% meets %.1f%% minimum\n", value, minimum }'

vet:
	$(GO) vet ./...

lint:
	$(GOLANGCI_LINT) run ./...

format-check:
	@test -z "$$($(GOFMT) -l .)" || { $(GOFMT) -l .; exit 1; }

tidy-check:
	$(GO) mod tidy -diff

check: format-check tidy-check coverage vet lint

generate:
	$(GO) generate ./...
