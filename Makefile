GO ?= go
SHELL=/bin/bash -o pipefail

default: build

.PHONY: build
build:
	mkdir -p ./bin/
	CGO_ENABLED=0 GOARCH=amd64 $(GO) build -o ./bin/incus-redfish-proxy.linux.amd64 ./cmd/incus-redfish-proxy
	CGO_ENABLED=0 GOARCH=arm64 $(GO) build -o ./bin/incus-redfish-proxy.linux.arm64 ./cmd/incus-redfish-proxy
	GOOS=darwin GOARCH=amd64 $(GO) build -o ./bin/incus-redfish-proxy.macos.amd64 ./cmd/incus-redfish-proxy
	GOOS=darwin GOARCH=arm64 $(GO) build -o ./bin/incus-redfish-proxy.macos.arm64 ./cmd/incus-redfish-proxy
	GOOS=windows GOARCH=amd64 $(GO) build -o ./bin/incus-redfish-proxy.windows.amd64.exe ./cmd/incus-redfish-proxy
	GOOS=windows GOARCH=arm64 $(GO) build -o ./bin/incus-redfish-proxy.windows.arm64.exe ./cmd/incus-redfish-proxy

# bld (build linux development)
# Build only the Linux AMD64 version, used for development and testing.
.PHONY: bld
bld:
	mkdir -p ./bin/
	CGO_ENABLED=0 GOARCH=amd64 $(GO) build -o ./bin/incus-redfish-proxy.linux.amd64 ./cmd/incus-redfish-proxy

.PHONY: build-redfish-scraper
build-redfish-scraper:
	mkdir -p ./bin/
	CGO_ENABLED=0 GOARCH=amd64 $(GO) build -o ./bin/redfish-scraper.linux.amd64 ./cmd/redfish-scraper
	CGO_ENABLED=0 GOARCH=arm64 $(GO) build -o ./bin/redfish-scraper.linux.arm64 ./cmd/redfish-scraper
	GOOS=darwin GOARCH=amd64 $(GO) build -o ./bin/redfish-scraper.macos.amd64 ./cmd/redfish-scraper
	GOOS=darwin GOARCH=arm64 $(GO) build -o ./bin/redfish-scraper.macos.arm64 ./cmd/redfish-scraper
	GOOS=windows GOARCH=amd64 $(GO) build -o ./bin/redfish-scraper.windows.amd64.exe ./cmd/redfish-scraper
	GOOS=windows GOARCH=arm64 $(GO) build -o ./bin/redfish-scraper.windows.arm64.exe ./cmd/redfish-scraper

.PHONY: build-all-packages
build-all-packages:
	$(GO) mod tidy
	$(GO) build ./...
	$(GO) test -c -o /dev/null ./...

.PHONY: test
test:
	$(GO) test ./... -v

.PHONY: static-analysis
static-analysis: license-check lint

.PHONY: license-check
license-check:
ifeq ($(shell command -v go-licenses),)
	(cd / ; $(GO) install -v -x github.com/google/go-licenses@latest)
endif
	go-licenses check --disallowed_types=forbidden,unknown,restricted --ignore github.com/rootless-containers/proto/go-proto --ignore github.com/FuturFusion/incus-redfish-proxy ./...

.PHONY: lint
lint:
ifeq ($(shell command -v golangci-lint),)
	curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b $$($(GO) env GOPATH)/bin
endif
	golangci-lint run ./...
	run-parts $(shell run-parts -V >/dev/null 2>&1 && echo -n "--verbose --exit-on-error --regex '\.sh$$'") scripts/lint

.PHONY: vulncheck
vulncheck:
ifeq ($(shell command -v govulncheck),)
	$(GO) install golang.org/x/vuln/cmd/govulncheck@latest
endif
	$(GO) run golang.org/x/vuln/cmd/govulncheck ./...

.PHONY: clean
clean:
	rm -rf coverage.out covdata-coverage.out covdata-coverage-func.out covdata-coverage-func-filtered.out
	rm -rf dist/ bin/

.PHONY: update-gomod
update-gomod:
	$(GO) get -t -v -u ./...
	$(GO) mod tidy --go=1.26.7
	$(GO) get toolchain@none
