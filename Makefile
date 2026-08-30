# reconc -- Repository Control Compiler (Go implementation)
#
# Targets:
#   make build              -- build the reconc binary for the host OS/arch
#   make test-fast          -- run cached root/template tests with bounded package parallelism
#   make test               -- run all tests uncached with -race
#   make test-langchain     -- run the pinned disposable LangChain proof
#   make fmt-check          -- reject unformatted non-ignored Go sources
#   make fmt                -- format all Go sources
#   make vet                -- run go vet
#   make lint               -- run pinned staticcheck
#   make coverage           -- measure root and template coverage
#   make cover              -- measure coverage and write root/template HTML reports
#   make fuzz               -- run every discovered Go fuzz target with a bounded budget
#   make clean              -- remove build artifacts + dist/
#   make run ARGS="--help"  -- build and run with args
#   make tidy               -- go mod tidy
#   make release            -- build release binaries for dist/ (darwin, linux, windows)
#   make completion         -- emit flat shell completion artifacts into dist/
#   make sbom               -- emit deterministic SPDX and CycloneDX SBOMs
#   make notices            -- emit deterministic third-party license notices
#   make checksums          -- generate dist/SHA256SUMS over release artefacts
#   make verify-release      -- verify dist/ against the canonical release matrix
#   make self-host          -- run the clean-repository bootstrap golden path
#   make publication-audit  -- scan the public tree and post-boundary history
#   make reference-docs     -- regenerate registry-owned Markdown reference sections
#   make reference-docs-check -- fail when generated Markdown references drift
#   make benchmark-record   -- record the calibrated benchmark suite
#   make benchmark-compare  -- compare a result with the checked baseline
#   make benchmark-baseline -- intentionally refresh the checked baseline

GO        ?= go
PYTHON    ?= python3
BIN       := reconc
PKG       := ./...
BINDIR    := .build/bin
DISTDIR   := dist
VERSION   ?= 0.9.8
PROVENANCE_PKG := reconc.dev/reconc/buildprovenance
STATICCHECK_VERSION := v0.8.1
TEST_PARALLELISM ?= 2
BENCHMARK_COUNT ?= 5
BENCHMARK_BENCHTIME ?= 100x
BENCHMARK_RESULT ?= .build/benchmarks/current.json
BENCHMARK_COMPARISON ?= .build/benchmarks/comparison.json
BENCHMARK_BASELINE ?= scripts/benchmarks/baseline.json
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

.PHONY: build test-fast test test-langchain test-release-trust self-host publication-audit harness-pack-check reference-docs reference-docs-check fmt-check fmt vet lint coverage cover fuzz clean run tidy release completion manpage sbom notices checksums verify-release bench benchmark-record benchmark-compare benchmark-baseline check-test-parallelism

build:
	@mkdir -p $(BINDIR)
	@goos=$$($(GO) env GOOS); \
	 goarch=$$($(GO) env GOARCH); \
	 marker=$$($(GO) run ./cmd/reconc-build-provenance --root . --goos "$$goos" --goarch "$$goarch" --version "$(VERSION)") || exit $$?; \
	 CGO_ENABLED=0 $(GO) build -trimpath \
	   -ldflags "-X main.Version=$(VERSION) -X $(PROVENANCE_PKG).BuildMarker=$$marker -s -w" \
	   -o $(BINDIR)/$(BIN) ./cmd/reconc; \
	 $(GO) run ./cmd/reconc-build-provenance --root . --goos "$$goos" --goarch "$$goarch" --version "$(VERSION)" --verify-binary $(BINDIR)/$(BIN)

check-test-parallelism:
	@case "$(TEST_PARALLELISM)" in \
	  ''|*[!0-9]*|0*) printf 'TEST_PARALLELISM must be a positive integer, got %s\n' "$(TEST_PARALLELISM)" >&2; exit 64 ;; \
	esac

test-fast: check-test-parallelism
	$(MAKE) --no-print-directory fmt-check
	$(MAKE) --no-print-directory reference-docs-check
	$(GO) test -p=$(TEST_PARALLELISM) $(PKG)
	(cd harness/template && $(GO) test -p=$(TEST_PARALLELISM) ./...)

test: check-test-parallelism
	$(MAKE) --no-print-directory fmt-check
	$(MAKE) --no-print-directory publication-audit
	$(GO) test -p=$(TEST_PARALLELISM) -race -count=1 -timeout 20m $(PKG)
	(cd harness/template && $(GO) test -p=$(TEST_PARALLELISM) -race -count=1 ./...)
	./scripts/tests/release-trust.sh

test-release-trust:
	./scripts/tests/release-trust.sh

test-langchain:
	PYTHON="$(PYTHON)" ./scripts/tests/langchain-integration.sh

self-host: build
	RECONC_BIN="$(CURDIR)/$(BINDIR)/$(BIN)" ./scripts/tests/self-hosting.sh

publication-audit:
	$(MAKE) --no-print-directory reference-docs-check
	$(GO) run ./scripts/audits/publication --root .
	$(MAKE) --no-print-directory harness-pack-check

harness-pack-check:
	$(GO) run ./scripts/build/harness-pack --check

reference-docs:
	$(GO) run ./scripts/build/reference-docs --root .

reference-docs-check:
	$(GO) run ./scripts/build/reference-docs --root . --check

fmt-check:
	@unformatted="$$(git ls-files -co --exclude-standard -z -- '*.go' | xargs -0 gofmt -l)"; \
	 test -z "$$unformatted" || { \
	   printf 'Go files require gofmt:\n%s\n' "$$unformatted" >&2; \
	   exit 1; \
	 }

fmt:
	$(GO) fmt $(PKG)
	(cd harness/template && $(GO) fmt ./...)

vet:
	$(GO) vet $(PKG)
	(cd harness/template && $(GO) vet ./...)

lint:
	$(GO) run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION) $(PKG)
	(cd harness/template && $(GO) run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION) ./...)

coverage: check-test-parallelism
	GO="$(GO)" TEST_PARALLELISM="$(TEST_PARALLELISM)" ./scripts/tests/coverage.sh

cover: check-test-parallelism
	GO="$(GO)" TEST_PARALLELISM="$(TEST_PARALLELISM)" ./scripts/tests/coverage.sh --html

fuzz:
	GO="$(GO)" ./scripts/tests/run-fuzz.sh

bench:
	$(GO) test -run '^$$' -bench . -benchmem -benchtime=1000x $(PKG)

benchmark-record:
	$(GO) run ./scripts/benchmarks/history record --root . --count $(BENCHMARK_COUNT) --benchtime $(BENCHMARK_BENCHTIME) --output $(BENCHMARK_RESULT)

benchmark-compare:
	$(GO) run ./scripts/benchmarks/history compare --baseline $(BENCHMARK_BASELINE) --result $(BENCHMARK_RESULT) --output $(BENCHMARK_COMPARISON)

benchmark-baseline:
	@test "$(CONFIRM_BENCHMARK_BASELINE)" = "1" || { printf 'Set CONFIRM_BENCHMARK_BASELINE=1 to refresh %s intentionally.\n' "$(BENCHMARK_BASELINE)" >&2; exit 64; }
	$(GO) run ./scripts/benchmarks/history baseline --result $(BENCHMARK_RESULT) --output $(BENCHMARK_BASELINE) --refresh

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
	@GO="$(GO)" ./scripts/release/generated-assets.sh generate completion "$(DISTDIR)" "$(VERSION)" "$(RELEASE_COMMIT)" "$(SOURCE_DATE_EPOCH)"
	@echo "completion scripts -> $(DISTDIR)/"

manpage:
	@mkdir -p $(DISTDIR)
	@GO="$(GO)" ./scripts/release/generated-assets.sh generate manpage "$(DISTDIR)" "$(VERSION)" "$(RELEASE_COMMIT)" "$(SOURCE_DATE_EPOCH)"
	@echo "man page -> $(DISTDIR)/reconc.1"

sbom:
	@mkdir -p $(DISTDIR)
	@GO="$(GO)" ./scripts/release/generated-assets.sh generate sbom "$(DISTDIR)" "$(VERSION)" "$(RELEASE_COMMIT)" "$(SOURCE_DATE_EPOCH)"
	@echo "SBOMs -> $(DISTDIR)/"

notices:
	@mkdir -p $(DISTDIR)
	@GO="$(GO)" ./scripts/release/generated-assets.sh generate notices "$(DISTDIR)" "$(VERSION)" "$(RELEASE_COMMIT)" "$(SOURCE_DATE_EPOCH)" $(RELEASE_TARGETS)
	@echo "license notices -> $(DISTDIR)/"

checksums: sbom notices
	@mkdir -p $(DISTDIR)
	@./scripts/release/copy-assets.sh $(DISTDIR)
	@./scripts/release/write-checksums.sh $(DISTDIR)
	@$(MAKE) --no-print-directory verify-release
	@echo "checksums -> $(DISTDIR)/SHA256SUMS"

verify-release:
	@GO="$(GO)" ./scripts/release/verify-artifacts.sh $(DISTDIR) $(BIN) $(VERSION) $(RELEASE_TARGETS)
