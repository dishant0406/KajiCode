# KajiCode build/test/lint targets. AGENTS.md says "Build with `make`" and "Run `make
# lint` before opening a PR" — these targets back those instructions.
.DEFAULT_GOAL := build
.PHONY: build build-all test test-race vet fmt fmt-check lint tidy clean baseline help

# Build the main CLI binary into ./kajicode.
build:
	go build -o kajicode ./cmd/kajicode

# Build every command in cmd/.
build-all:
	go build ./...

# Run the full test suite with the race detector (matches CI expectations).
test:
	go test ./... -race -count=1

# Faster, no race detector.
test-quick:
	go test ./...

vet:
	go vet ./...

fmt:
	@git ls-files --cached --others --exclude-standard '*.go' | while IFS= read -r file; do \
		[ ! -f "$$file" ] || gofmt -w "$$file"; \
	done

# Fail if any tracked or untracked Go file is not gofmt-clean.
fmt-check:
	@out="$$(git ls-files --cached --others --exclude-standard '*.go' | while IFS= read -r file; do \
		[ ! -f "$$file" ] || gofmt -l "$$file"; \
	done)"; \
	if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

# Lint = formatting check + vet (no extra tooling required).
lint: fmt-check vet

tidy:
	go mod tidy

clean:
	rm -f kajicode
	go clean ./...

# Run the per-turn benchmark harness over the checked-in baseline manifest and
# write the JSON result to internal/perfbench/reports/baseline.json. Requires a
# built `kajicode` binary and a model; set KAJICODE_BENCH_MODEL (required) and
# KAJICODE_BENCH_BINARY (defaults to ./kajicode) to configure the run. The report is
# machine-specific and regenerated, not hand-edited.
baseline: build
	@if [ -z "$(KAJICODE_BENCH_MODEL)" ]; then echo "Set KAJICODE_BENCH_MODEL (and optionally KAJICODE_BENCH_BINARY) before running 'make baseline'"; exit 2; fi
	@KAJICODE_BIN="$${KAJICODE_BENCH_BINARY:-./kajicode}"; \
	go run ./cmd/kajicode-perf-bench turn \
		--suite internal/perfbench/manifests/baseline.json \
		--model $(KAJICODE_BENCH_MODEL) \
		--binary "$$KAJICODE_BIN" \
		--output internal/perfbench/reports/baseline.json

help:
	@echo "Targets: build (default), build-all, test, test-quick, vet, fmt, fmt-check, lint, tidy, clean, baseline"
