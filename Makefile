# reconc -- Repository Control Compiler (Go implementation)
#
# Targets:
#   make build              -- build the reconc binary for the host OS/arch
#   make test               -- run all tests with -race
#   make fmt                -- format all Go sources
#   make vet                -- run go vet
#   make lint               -- run staticcheck (if installed)
#   make cover              -- tests with coverage -> coverage.html
#   make clean              -- remove build artifacts + dist/
#   make run ARGS="--help"  -- build and run with args
#   make tidy               -- go mod tidy
#   make release            -- build release binaries for dist/ (darwin, linux, windows)
#   make completion         -- emit flat shell completion artifacts into dist/
#   make checksums          -- generate dist/SHA256SUMS over release artefacts
#   make self-host          -- run the clean-repository bootstrap golden path

GO        ?= go
BIN       := reconc
PKG       := ./...
BINDIR    := .build/bin
DISTDIR   := dist
VERSION   ?= 0.7.0
LDFLAGS   := -ldflags "-X main.Version=$(VERSION) -s -w"
STATICCHECK_VERSION := v0.7.0

# Release matrix. Each entry is OS/ARCH separated by '/'. Extend here
# to ship another platform -- nothing else needs to change.
RELEASE_TARGETS := \
	darwin/amd64 \
	darwin/arm64 \
	linux/amd64 \
	linux/arm64 \
	windows/amd64

.PHONY: build test test-release-trust self-host fmt vet lint cover clean run tidy release completion manpage checksums release-all bench

build:
	@mkdir -p $(BINDIR)
	$(GO) build $(LDFLAGS) -o $(BINDIR)/$(BIN) ./cmd/reconc

test:
	$(GO) test -race -count=1 $(PKG)
	./scripts/tests/release-trust.sh

test-release-trust:
	./scripts/tests/release-trust.sh

self-host: build
	RECONC_BIN="$(CURDIR)/$(BINDIR)/$(BIN)" ./scripts/tests/self-hosting.sh

fmt:
	$(GO) fmt $(PKG)

vet:
	$(GO) vet $(PKG)

lint:
	$(GO) run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION) $(PKG)

cover:
	$(GO) test -cover -coverprofile=coverage.out $(PKG)
	$(GO) tool cover -html=coverage.out -o coverage.html

bench:
	$(GO) test -run '^$$' -bench . -benchmem -benchtime=1000x $(PKG)

clean:
	rm -rf .build $(DISTDIR) coverage.out coverage.html

run: build
	$(BINDIR)/$(BIN) $(ARGS)

tidy:
	$(GO) mod tidy

# Cross-compile one target from RELEASE_TARGETS. Invoked by `release`.
# Usage: make release-one TARGET=darwin/arm64
.PHONY: release-one
release-one:
	@mkdir -p $(DISTDIR)
	@os=$${TARGET%/*}; arch=$${TARGET##*/}; \
	 ext=$$( [ "$$os" = "windows" ] && echo ".exe" || echo "" ); \
	 out=$(DISTDIR)/$(BIN)-$(VERSION)-$$os-$$arch$$ext; \
	 echo "building $$out"; \
	 GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 \
	   $(GO) build $(LDFLAGS) -trimpath -o $$out ./cmd/reconc

release: clean
	@mkdir -p $(DISTDIR)
	@set -eu; for t in $(RELEASE_TARGETS); do \
	  $(MAKE) --no-print-directory release-one TARGET=$$t; \
	done
	@$(MAKE) --no-print-directory completion
	@$(MAKE) --no-print-directory manpage
	@$(MAKE) --no-print-directory checksums
	@echo
	@echo "Release $(VERSION) ready in $(DISTDIR)/:"
	@ls -1 $(DISTDIR)

completion:
	@mkdir -p $(DISTDIR)
	@$(GO) run ./cmd/reconc completion bash > $(DISTDIR)/reconc.bash
	@$(GO) run ./cmd/reconc completion zsh  > $(DISTDIR)/_reconc
	@$(GO) run ./cmd/reconc completion fish > $(DISTDIR)/reconc.fish
	@echo "completion scripts -> $(DISTDIR)/"

manpage:
	@mkdir -p $(DISTDIR)
	@$(GO) run ./cmd/reconc manpage > $(DISTDIR)/reconc.1
	@echo "man page -> $(DISTDIR)/reconc.1"

checksums:
	@mkdir -p $(DISTDIR)
	@cp schemas/v1/*.schema.json $(DISTDIR)/
	@./scripts/release/write-checksums.sh $(DISTDIR)
	@./scripts/release/verify-artifacts.sh $(DISTDIR) $(BIN) $(VERSION) $(RELEASE_TARGETS)
	@echo "checksums -> $(DISTDIR)/SHA256SUMS"
