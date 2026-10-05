GO ?= go
VERSION ?= dev
GOOS ?= $(shell $(GO) env GOOS)
GOARCH ?= $(shell $(GO) env GOARCH)

.PHONY: build run demo test race vet fmt fmt-check check smoke vuln package

build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "-X main.version=$(VERSION)" -o bin/tuigram ./cmd/tuigram

run: build
	./bin/tuigram

demo: build
	./bin/tuigram --demo

test:
	$(GO) test ./...

race:
	$(GO) test -race -cover ./...

vet:
	$(GO) vet ./...

fmt:
	gofmt -w cmd internal

fmt-check:
	@unformatted=$$(gofmt -l cmd internal) || exit $$?; \
	if [ -n "$$unformatted" ]; then \
		printf '%s\n' "$$unformatted"; \
		exit 1; \
	fi

check: fmt-check vet race smoke

smoke: build
	bash scripts/smoke.sh ./bin/tuigram

vuln:
	$(GO) run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...

package:
	bash scripts/package.sh $(GOOS) $(GOARCH) $(VERSION)
