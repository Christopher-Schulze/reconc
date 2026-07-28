# reconc -- Repository Control Compiler (Go implementation)
#
# Targets:
#   make build              -- build the reconc binary for the host OS/arch
#   make test               -- run all tests with -race
#   make fmt                -- format all Go sources
#   make vet                -- run go vet
#   make lint               -- run pinned staticcheck
#   make coverage           -- enforce root and template coverage floors
#   make cover              -- coverage gate plus root and template HTML reports
#   make clean              -- remove build artifacts + dist/
#   make run ARGS="--help"  -- build and run with args
#   make tidy               -- go mod tidy
#   make release            -- build release binaries for dist/ (darwin, linux, windows)
#   make completion         -- emit flat shell completion artifacts into dist/
#   make sbom               -- emit deterministic SPDX and CycloneDX SBOMs
#   make checksums          -- generate dist/SHA256SUMS over release artefacts
#   make self-host          -- run the clean-repository bootstrap golden path
#   make publication-audit  -- scan the public tree and post-boundary history

GO        ?= go
BIN       := reconc
PKG       := ./...
BINDIR    := .build/bin
DISTDIR   := dist
VERSION   ?= 0.9.1
PROVENANCE_PKG := reconc.dev/reconc/buildprovenance
STATICCHECK_VERSION := v0.7.0
RELEASE_COMMIT ?= $(shell git rev-parse HEAD)
SOURCE_DATE_EPOCH ?= $(shell git show -s --format=%ct $(RELEASE_COMMIT))

# Release matrix. Each entry is OS/ARCH separated by '/'. Extend here
# to ship another platform -- nothing else needs to change.
RELEASE_TARGETS := \
	darwin/amd64 \
	darwin/arm64 \
	linux/amd64 \
	linux/arm64 \
	windows/amd64

.PHONY: build test test-release-trust self-host publication-audit harness-pack-check fmt vet lint coverage cover clean run tidy release completion manpage sbom checksums release-all bench

build:
	@mkdir -p $(BINDIR)
	@goos=$$($(GO) env GOOS); \
	 goarch=$$($(GO) env GOARCH); \
	 marker=$$($(GO) run ./cmd/reconc-build-provenance --root . --goos "$$goos" --goarch "$$goarch" --version "$(VERSION)") || exit $$?; \
	 CGO_ENABLED=0 $(GO) build -trimpath \
	   -ldflags "-X main.Version=$(VERSION) -X $(PROVENANCE_PKG).BuildMarker=$$marker -s -w" \
	   -o $(BINDIR)/$(BIN) ./cmd/reconc; \
	 $(GO) run ./cmd/reconc-build-provenance --root . --goos "$$goos" --goarch "$$goarch" --version "$(VERSION)" --verify-binary $(BINDIR)/$(BIN)

test:
	$(MAKE) --no-print-directory publication-audit
	$(GO) test -race -count=1 $(PKG)
	(cd harness/template && $(GO) test -race -count=1 ./...)
	./scripts/tests/release-trust.sh

test-release-trust:
	./scripts/tests/release-trust.sh

self-host: build
	RECONC_BIN="$(CURDIR)/$(BINDIR)/$(BIN)" ./scripts/tests/self-hosting.sh

publication-audit:
	$(GO) run ./scripts/audits/publication --root .
	$(MAKE) --no-print-directory harness-pack-check

harness-pack-check:
	$(GO) run ./scripts/build/harness-pack --check

fmt:
	$(GO) fmt $(PKG)
	(cd harness/template && $(GO) fmt ./...)

vet:
	$(GO) vet $(PKG)
	(cd harness/template && $(GO) vet ./...)

lint:
	$(GO) run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION) $(PKG)
	(cd harness/template && $(GO) run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION) ./...)

coverage:
	GO="$(GO)" ./scripts/tests/coverage.sh

cover:
	GO="$(GO)" ./scripts/tests/coverage.sh --html

bench:
	$(GO) test -run '^$$' -bench . -benchmem -benchtime=1000x $(PKG)

clean:
	rm -rf .build $(DISTDIR) coverage.out coverage.html harness/template/coverage.out harness/template/coverage.html

run: build
	$(BINDIR)/$(BIN) $(ARGS)

tidy:
	$(GO) mod tidy
	(cd harness/template && $(GO) mod tidy)

# Cross-compile one target from RELEASE_TARGETS. Invoked by `release`.
# Usage: make release-one TARGET=darwin/arm64
.PHONY: release-one
release-one:
	@mkdir -p $(DISTDIR)
	@os=$${TARGET%/*}; arch=$${TARGET##*/}; \
	 ext=$$( [ "$$os" = "windows" ] && echo ".exe" || echo "" ); \
	 out=$(DISTDIR)/$(BIN)-$(VERSION)-$$os-$$arch$$ext; \
	 marker=$$($(GO) run ./cmd/reconc-build-provenance --root . --goos "$$os" --goarch "$$arch" --version "$(VERSION)") || exit $$?; \
	 echo "building $$out"; \
	 GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 \
	   $(GO) build -trimpath \
	     -ldflags "-X main.Version=$(VERSION) -X $(PROVENANCE_PKG).BuildMarker=$$marker -s -w" \
	     -o $$out ./cmd/reconc; \
	 $(GO) run ./cmd/reconc-build-provenance --root . --goos "$$os" --goarch "$$arch" --version "$(VERSION)" --verify-binary $$out

release: publication-audit clean
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
	@SOURCE_DATE_EPOCH="$(SOURCE_DATE_EPOCH)" $(GO) run -ldflags "-X main.Version=$(VERSION)" ./cmd/reconc manpage > $(DISTDIR)/reconc.1
	@echo "man page -> $(DISTDIR)/reconc.1"

sbom:
	@$(GO) run ./scripts/release/sbom generate --root . --output-dir $(DISTDIR) --version $(VERSION) --commit $(RELEASE_COMMIT) --source-date-epoch $(SOURCE_DATE_EPOCH)
	@echo "SBOMs -> $(DISTDIR)/"

checksums: sbom
	@mkdir -p $(DISTDIR)
	@cp install.sh install.ps1 $(DISTDIR)/
	@cp harness/advanced-pack.zip $(DISTDIR)/reconc-harness-pack-advanced-1.0.0.zip
	@cp schemas/v1/completion-report.schema.json schemas/v1/global-diagnostic.schema.json schemas/v1/global-lifecycle.schema.json schemas/v1/harness-pack-manifest.schema.json schemas/v1/installation-receipt.schema.json schemas/v1/policy-config.schema.json schemas/v1/policy-fix-plan.schema.json schemas/v1/policy-report.schema.json schemas/v1/proof-bundle.schema.json schemas/v1/release-manifest.schema.json schemas/v1/repository-install.schema.json schemas/v1/repository-sync-plan.schema.json schemas/v1/repository-sync-report.schema.json $(DISTDIR)/
	@cp schemas/v1/policy-lock.schema.json $(DISTDIR)/policy-lock-v1.schema.json
	@cp schemas/v2/policy-lock.schema.json $(DISTDIR)/policy-lock.schema.json
	@./scripts/release/write-checksums.sh $(DISTDIR)
	@GO="$(GO)" ./scripts/release/verify-artifacts.sh $(DISTDIR) $(BIN) $(VERSION) $(RELEASE_TARGETS)
	@echo "checksums -> $(DISTDIR)/SHA256SUMS"
