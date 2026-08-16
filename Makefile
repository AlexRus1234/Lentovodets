.SILENT:

GO ?= go
GOLANGCI_LINT ?= golangci-lint
NPM ?= npm

PKG := ./...
COVER_OUT := coverage/coverage.out
COVER_THRESHOLD := 90

.PHONY: all
all: lint test build

.PHONY: lint
lint:
	"$(GOLANGCI_LINT)" run ./...

.PHONY: vet
vet:
	"$(GO)" vet ./...

.PHONY: fmt
fmt:
	"$(GOLANGCI_LINT)" run --fix ./...

.PHONY: test
test:
	"$(GO)" test ./...

.PHONY: test-race
test-race:
	"$(GO)" test -race ./...

.PHONY: cover
cover:
	"$(GO)" test -coverprofile="$(COVER_OUT)" ./...
	"$(GO)" tool cover -func="$(COVER_OUT)" | tail -n 1

.PHONY: build
build:
	"$(GO)" build -o bin/lentovodec ./cmd/lentovodec

.PHONY: build-tape
build-tape:
	"$(GO)" build -tags tape -o bin/lentovodec ./cmd/lentovodec

.PHONY: web-build
web-build:
	cd web && "$(NPM)" install && "$(NPM)" run build

.PHONY: web-dev
web-dev:
	cd web && "$(NPM)" run dev

.PHONY: clean
clean:
	$(GO) clean
	rm -rf bin coverage dist
	rm -rf internal/iface/web/assets
	mkdir -p internal/iface/web/assets
	printf '<!doctype html>\n<html lang="ru"><head><meta charset="utf-8"><title>lentovodec</title></head><body><p>Web UI не собран. Выполните <code>make web-build</code> (Этап 8) и перезапустите демона.</p></body></html>\n' > internal/iface/web/assets/index.html
