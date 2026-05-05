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
#   make completion         -- emit shell completion scripts into dist/completion/
#   make checksums          -- generate dist/SHA256SUMS over release artefacts

GO        ?= go
BIN       := reconc
PKG       := ./...
BINDIR    := bin
DISTDIR   := dist
VERSION   ?= 0.4.0
LDFLAGS   := -ldflags "-X main.Version=$(VERSION) -s -w"

# Release matrix. Each entry is OS/ARCH separated by '/'. Extend here
# to ship another platform -- nothing else needs to change.
RELEASE_TARGETS := \
	darwin/amd64 \
	darwin/arm64 \
	linux/amd64 \
	linux/arm64 \
	windows/amd64

.PHONY: build test fmt vet lint cover clean run tidy release completion manpage checksums release-all bench

build:
	$(GO) build $(LDFLAGS) -o $(BINDIR)/$(BIN) ./cmd/reconc

test:
	$(GO) test -race -count=1 $(PKG)

fmt:
	$(GO) fmt $(PKG)

vet:
	$(GO) vet $(PKG)

lint:
	$(GO) run honnef.co/go/tools/cmd/staticcheck@latest $(PKG)

cover:
	$(GO) test -cover -coverprofile=coverage.out $(PKG)
	$(GO) tool cover -html=coverage.out -o coverage.html

bench:
	$(GO) test -run '^$$' -bench . -benchmem -benchtime=1000x $(PKG)

clean:
	rm -rf $(BINDIR) $(DISTDIR) coverage.out coverage.html

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
	@for t in $(RELEASE_TARGETS); do \
	  $(MAKE) --no-print-directory release-one TARGET=$$t; \
	done
	@$(MAKE) --no-print-directory completion
	@$(MAKE) --no-print-directory manpage
	@$(MAKE) --no-print-directory checksums
	@echo
	@echo "Release $(VERSION) ready in $(DISTDIR)/:"
	@ls -1 $(DISTDIR)

completion:
	@mkdir -p $(DISTDIR)/completion
	@$(GO) run ./cmd/reconc completion bash > $(DISTDIR)/completion/reconc.bash
	@$(GO) run ./cmd/reconc completion zsh  > $(DISTDIR)/completion/_reconc
	@$(GO) run ./cmd/reconc completion fish > $(DISTDIR)/completion/reconc.fish
	@echo "completion scripts -> $(DISTDIR)/completion/"

manpage:
	@mkdir -p $(DISTDIR)/man
	@$(GO) run ./cmd/reconc manpage > $(DISTDIR)/man/reconc.1
	@echo "man page -> $(DISTDIR)/man/reconc.1"

checksums:
	@cd $(DISTDIR) && shasum -a 256 $(BIN)-$(VERSION)-* > SHA256SUMS 2>/dev/null || true
	@echo "checksums -> $(DISTDIR)/SHA256SUMS"
