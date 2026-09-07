.DEFAULT_GOAL := build

BINARY := skillmod
GO ?= go
GOLANGCI_LINT ?= golangci-lint
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

.PHONY: build install test vet lint check generate

build:
	$(GO) build -trimpath -ldflags "$(LD_FLAGS)" -o ./$(BINARY)$(EXE) .

install:
	GOBIN="$(INSTALL_DIR)" $(GO) install -trimpath -ldflags "$(LD_FLAGS)" .
	@printf 'installed $(BINARY)$(EXE) to %s\n' "$(INSTALL_DIR)"

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

lint:
	$(GOLANGCI_LINT) run --new-from-rev HEAD ./...

check: test vet lint

generate:
	$(GO) generate ./...
